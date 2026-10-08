package k8s

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	lrucache "github.com/hashicorp/golang-lru/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	batchlisters "k8s.io/client-go/listers/batch/v1"
	"k8s.io/client-go/tools/cache"
)

const MAX_RESOLVED_DNS = 10000 // arbitrary limit

// Keeps a deleted pod's IPs resolvable for connections that are still reported after the pod is gone.
var deletedPodIPRetention = 2 * time.Minute

var watchEventsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "caretta_watcher_events_count",
}, []string{"object_type"})

type Workload struct {
	Name      string
	Namespace string
	Kind      string
	Owner     string
}

type ipEntry struct {
	workload Workload
	// UID of the object that registered the IP, so a deletion never removes a newer owner's entry.
	uid types.UID
}

type K8sIPResolver struct {
	clientset           kubernetes.Interface
	shouldResolveDns    bool
	traverseUpHierarchy bool
	dnsResolvedIps      *lrucache.Cache[string, string]

	ipsMu sync.RWMutex
	ips   map[string]ipEntry

	podDescriptors sync.Map // types.UID -> Workload

	stopCh   chan struct{}
	stopOnce sync.Once

	replicaSets  appslisters.ReplicaSetLister
	daemonSets   appslisters.DaemonSetLister
	statefulSets appslisters.StatefulSetLister
	deployments  appslisters.DeploymentLister
	jobs         batchlisters.JobLister
	cronJobs     batchlisters.CronJobLister
}

func NewK8sIPResolver(clientset kubernetes.Interface, resolveDns bool, traverseUpHierarchy bool) (*K8sIPResolver, error) {
	var dnsCache *lrucache.Cache[string, string]
	if resolveDns {
		var err error
		dnsCache, err = lrucache.New[string, string](MAX_RESOLVED_DNS)
		if err != nil {
			return nil, err
		}
	}
	return &K8sIPResolver{
		clientset:           clientset,
		shouldResolveDns:    resolveDns,
		traverseUpHierarchy: traverseUpHierarchy,
		dnsResolvedIps:      dnsCache,
		ips:                 make(map[string]ipEntry),
		stopCh:              make(chan struct{}),
	}, nil
}

// resolve the given IP from the resolver's cache
// if not available, return the IP itself.
func (resolver *K8sIPResolver) ResolveIP(ip string) Workload {
	resolver.ipsMu.RLock()
	entry, ok := resolver.ips[ip]
	resolver.ipsMu.RUnlock()
	if ok {
		return entry.workload
	}

	host := ip
	if resolver.shouldResolveDns {
		val, ok := resolver.dnsResolvedIps.Get(ip)
		if ok {
			host = val
		} else {
			hosts, err := net.LookupAddr(ip)
			if err == nil && len(hosts) > 0 {
				host = hosts[0]
			}
			resolver.dnsResolvedIps.Add(ip, host)
		}
	}
	return Workload{
		Name:      host,
		Namespace: "external",
		Kind:      "external",
	}
}

// StartWatching returns once every cache is synced and the initial IP mapping is built.
func (resolver *K8sIPResolver) StartWatching() error {
	factory := informers.NewSharedInformerFactoryWithOptions(resolver.clientset, 0, informers.WithTransform(trimForCache))

	pods := factory.Core().V1().Pods().Informer()
	nodes := factory.Core().V1().Nodes().Informer()
	services := factory.Core().V1().Services().Informer()
	resolver.replicaSets = factory.Apps().V1().ReplicaSets().Lister()
	resolver.daemonSets = factory.Apps().V1().DaemonSets().Lister()
	resolver.statefulSets = factory.Apps().V1().StatefulSets().Lister()
	resolver.deployments = factory.Apps().V1().Deployments().Lister()
	resolver.jobs = factory.Batch().V1().Jobs().Lister()
	resolver.cronJobs = factory.Batch().V1().CronJobs().Lister()

	factory.Start(resolver.stopCh)
	for informerType, synced := range factory.WaitForCacheSync(resolver.stopCh) {
		if !synced {
			resolver.StopWatching()
			return fmt.Errorf("syncing %v cache", informerType)
		}
	}

	// Handlers are added only after every cache has synced, so the replayed initial pods can resolve their owners.
	podsReg, err := pods.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    resolver.onPodUpsert,
		UpdateFunc: func(_, obj any) { resolver.onPodUpsert(obj) },
		DeleteFunc: resolver.onPodDelete,
	})
	if err != nil {
		return err
	}
	nodesReg, err := nodes.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    resolver.onNodeUpsert,
		UpdateFunc: func(_, obj any) { resolver.onNodeUpsert(obj) },
		DeleteFunc: resolver.onNodeDelete,
	})
	if err != nil {
		return err
	}
	servicesReg, err := services.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    resolver.onServiceUpsert,
		UpdateFunc: func(_, obj any) { resolver.onServiceUpsert(obj) },
		DeleteFunc: resolver.onServiceDelete,
	})
	if err != nil {
		return err
	}

	if !cache.WaitForCacheSync(resolver.stopCh, podsReg.HasSynced, nodesReg.HasSynced, servicesReg.HasSynced) {
		resolver.StopWatching()
		return errors.New("building initial IP mapping")
	}
	return nil
}

func (resolver *K8sIPResolver) StopWatching() {
	resolver.stopOnce.Do(func() { close(resolver.stopCh) })
}

func (resolver *K8sIPResolver) onPodUpsert(obj any) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}
	watchEventsCounter.WithLabelValues("pod").Inc()
	workload := resolver.resolvePodDescriptor(pod)
	for _, podIP := range pod.Status.PodIPs {
		resolver.storeWorkloadsIP(podIP.IP, workload, pod.UID)
	}
}

func (resolver *K8sIPResolver) onPodDelete(obj any) {
	pod, ok := unwrapTombstone(obj).(*v1.Pod)
	if !ok {
		return
	}
	watchEventsCounter.WithLabelValues("pod").Inc()
	resolver.podDescriptors.Delete(pod.UID)

	ips := make([]string, 0, len(pod.Status.PodIPs))
	for _, podIP := range pod.Status.PodIPs {
		ips = append(ips, podIP.IP)
	}
	time.AfterFunc(deletedPodIPRetention, func() { resolver.removeIPs(ips, pod.UID) })
}

func (resolver *K8sIPResolver) onNodeUpsert(obj any) {
	node, ok := obj.(*v1.Node)
	if !ok {
		return
	}
	watchEventsCounter.WithLabelValues("node").Inc()
	workload := Workload{
		Name:      node.Name,
		Namespace: "node",
		Kind:      "node",
	}
	for _, address := range node.Status.Addresses {
		resolver.storeWorkloadsIP(address.Address, workload, node.UID)
	}
}

func (resolver *K8sIPResolver) onNodeDelete(obj any) {
	node, ok := unwrapTombstone(obj).(*v1.Node)
	if !ok {
		return
	}
	watchEventsCounter.WithLabelValues("node").Inc()
	ips := make([]string, 0, len(node.Status.Addresses))
	for _, address := range node.Status.Addresses {
		ips = append(ips, address.Address)
	}
	resolver.removeIPs(ips, node.UID)
}

func (resolver *K8sIPResolver) onServiceUpsert(obj any) {
	service, ok := obj.(*v1.Service)
	if !ok {
		return
	}
	watchEventsCounter.WithLabelValues("service").Inc()
	// TODO maybe try to match service to workload
	workload := Workload{
		Name:      service.Name,
		Namespace: service.Namespace,
		Kind:      "Service",
	}
	for _, clusterIP := range service.Spec.ClusterIPs {
		if clusterIP != "" && clusterIP != v1.ClusterIPNone {
			resolver.storeWorkloadsIP(clusterIP, workload, service.UID)
		}
	}
}

func (resolver *K8sIPResolver) onServiceDelete(obj any) {
	service, ok := unwrapTombstone(obj).(*v1.Service)
	if !ok {
		return
	}
	watchEventsCounter.WithLabelValues("service").Inc()
	resolver.removeIPs(service.Spec.ClusterIPs, service.UID)
}

func (resolver *K8sIPResolver) storeWorkloadsIP(ip string, workload Workload, uid types.UID) {
	resolver.ipsMu.Lock()
	defer resolver.ipsMu.Unlock()
	if existing, ok := resolver.ips[ip]; ok {
		// Node addresses win over hostNetwork pods sharing them, and services never displace another object.
		if existing.workload.Kind == "node" && workload.Kind != "node" {
			return
		}
		if workload.Kind == "Service" && existing.uid != uid {
			return
		}
	}
	resolver.ips[ip] = ipEntry{workload: workload, uid: uid}
}

func (resolver *K8sIPResolver) removeIPs(ips []string, uid types.UID) {
	resolver.ipsMu.Lock()
	defer resolver.ipsMu.Unlock()
	for _, ip := range ips {
		if existing, ok := resolver.ips[ip]; ok && existing.uid == uid {
			delete(resolver.ips, ip)
		}
	}
}

func (resolver *K8sIPResolver) resolvePodDescriptor(pod *v1.Pod) Workload {
	if existing, ok := resolver.podDescriptors.Load(pod.UID); ok {
		return existing.(Workload)
	}

	var err error
	name := pod.Name
	kind := "pod"
	result := Workload{
		Name:      name,
		Namespace: pod.Namespace,
		Kind:      kind,
	}

	if resolver.traverseUpHierarchy {
		owner := metav1.GetControllerOf(pod)
		// climbing up the owners' hierarchy. if an error occurs, we take the data we got and
		// skip caching so the resolution is retried on the pod's next update.
		for owner != nil {
			name = owner.Name
			kind = owner.Kind
			owner, err = resolver.getControllerOfOwner(pod.Namespace, owner)
			if err != nil {
				log.Printf("Warning: couldn't retrieve owner of %v - %v", name, err)
			}
		}
		result.Name = name
		result.Kind = kind
	} else if owner := metav1.GetControllerOf(pod); owner != nil {
		result.Owner = owner.Name
	}

	if err == nil {
		resolver.podDescriptors.Store(pod.UID, result)
	}
	return result
}

// Owners are always in the same namespace as the object they control.
func (resolver *K8sIPResolver) getControllerOfOwner(namespace string, owner *metav1.OwnerReference) (*metav1.OwnerReference, error) {
	switch owner.Kind {
	case "ReplicaSet":
		obj, err := resolver.replicaSets.ReplicaSets(namespace).Get(owner.Name)
		return controllerOf(obj, err, owner)
	case "DaemonSet":
		obj, err := resolver.daemonSets.DaemonSets(namespace).Get(owner.Name)
		return controllerOf(obj, err, owner)
	case "StatefulSet":
		obj, err := resolver.statefulSets.StatefulSets(namespace).Get(owner.Name)
		return controllerOf(obj, err, owner)
	case "Deployment":
		obj, err := resolver.deployments.Deployments(namespace).Get(owner.Name)
		return controllerOf(obj, err, owner)
	case "Job":
		obj, err := resolver.jobs.Jobs(namespace).Get(owner.Name)
		return controllerOf(obj, err, owner)
	case "CronJob":
		obj, err := resolver.cronJobs.CronJobs(namespace).Get(owner.Name)
		return controllerOf(obj, err, owner)
	case "Node":
		// Static (mirror) pods are owned by their node, which has no controller.
		return nil, nil
	}
	return nil, errors.New("Unsupported kind for lookup - " + owner.Kind)
}

func controllerOf[T metav1.Object](obj T, err error, ref *metav1.OwnerReference) (*metav1.OwnerReference, error) {
	if err != nil {
		return nil, fmt.Errorf("missing %s for UID %s: %w", ref.Kind, ref.UID, err)
	}
	if obj.GetUID() != ref.UID {
		return nil, fmt.Errorf("%s %s has UID %s, expected %s", ref.Kind, ref.Name, obj.GetUID(), ref.UID)
	}
	return metav1.GetControllerOf(obj), nil
}

func unwrapTombstone(obj any) any {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return tombstone.Obj
	}
	return obj
}

// trimForCache drops every field the resolver doesn't read before objects enter the informer caches.
func trimForCache(obj any) (any, error) {
	if m, err := meta.Accessor(obj); err == nil {
		m.SetManagedFields(nil)
		m.SetAnnotations(nil)
	}
	switch o := obj.(type) {
	case *v1.Pod:
		o.Spec = v1.PodSpec{}
		o.Status = v1.PodStatus{Phase: o.Status.Phase, PodIPs: o.Status.PodIPs}
	case *v1.Node:
		o.Spec = v1.NodeSpec{}
		o.Status = v1.NodeStatus{Addresses: o.Status.Addresses}
	case *v1.Service:
		o.Spec = v1.ServiceSpec{ClusterIPs: o.Spec.ClusterIPs}
		o.Status = v1.ServiceStatus{}
	case *appsv1.ReplicaSet:
		o.Spec, o.Status = appsv1.ReplicaSetSpec{}, appsv1.ReplicaSetStatus{}
	case *appsv1.DaemonSet:
		o.Spec, o.Status = appsv1.DaemonSetSpec{}, appsv1.DaemonSetStatus{}
	case *appsv1.StatefulSet:
		o.Spec, o.Status = appsv1.StatefulSetSpec{}, appsv1.StatefulSetStatus{}
	case *appsv1.Deployment:
		o.Spec, o.Status = appsv1.DeploymentSpec{}, appsv1.DeploymentStatus{}
	case *batchv1.Job:
		o.Spec, o.Status = batchv1.JobSpec{}, batchv1.JobStatus{}
	case *batchv1.CronJob:
		o.Spec, o.Status = batchv1.CronJobSpec{}, batchv1.CronJobStatus{}
	}
	return obj, nil
}
