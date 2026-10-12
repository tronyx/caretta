package k8s

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
)

func newTestResolver(t *testing.T, traverse bool) *K8sIPResolver {
	resolver, err := NewK8sIPResolver(nil, false, traverse)
	assert.NoError(t, err)
	return resolver
}

func external(ip string) Workload {
	return Workload{Name: ip, Namespace: "external", Kind: "external"}
}

func testPod(name, uid, ip string, owner *metav1.OwnerReference) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(uid)},
		Status:     v1.PodStatus{PodIPs: []v1.PodIP{{IP: ip}}},
	}
	if owner != nil {
		pod.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	return pod
}

func testNode(name, uid, ip string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(uid)},
		Status:     v1.NodeStatus{Addresses: []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: ip}}},
	}
}

func testService(name, uid string, ips ...string) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(uid)},
		Spec:       v1.ServiceSpec{ClusterIPs: ips},
	}
}

func controllerRef(kind, name, uid string) *metav1.OwnerReference {
	isController := true
	return &metav1.OwnerReference{Kind: kind, Name: name, UID: types.UID(uid), Controller: &isController}
}

func shortRetention(t *testing.T) {
	original := deletedPodIPRetention
	deletedPodIPRetention = 20 * time.Millisecond
	t.Cleanup(func() { deletedPodIPRetention = original })
}

func TestDeletedPodIPIsKeptBrieflyThenForgotten(t *testing.T) {
	shortRetention(t)
	r := newTestResolver(t, false)
	pod := testPod("web", "uid-1", "10.0.0.1", nil)
	r.onPodUpsert(pod)

	r.onPodDelete(pod)
	assert.Equal(t, "web", r.ResolveIP("10.0.0.1").Name, "late connections from a deleted pod still resolve")
	assert.Eventually(t, func() bool { return r.ResolveIP("10.0.0.1") == external("10.0.0.1") }, time.Second, 5*time.Millisecond)
}

func TestDeletedPodNeverRemovesItsIPsNewOwner(t *testing.T) {
	shortRetention(t)
	r := newTestResolver(t, false)
	old := testPod("old", "uid-old", "10.0.0.1", nil)
	r.onPodUpsert(old)
	r.onPodDelete(old)
	r.onPodUpsert(testPod("new", "uid-new", "10.0.0.1", nil))

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, "new", r.ResolveIP("10.0.0.1").Name)
}

func TestPodDeleteFromATombstone(t *testing.T) {
	shortRetention(t)
	r := newTestResolver(t, false)
	pod := testPod("web", "uid-1", "10.0.0.1", nil)
	r.onPodUpsert(pod)

	r.onPodDelete(cache.DeletedFinalStateUnknown{Key: "ns/web", Obj: pod})
	assert.Eventually(t, func() bool { return r.ResolveIP("10.0.0.1") == external("10.0.0.1") }, time.Second, 5*time.Millisecond)
	_, cached := r.podDescriptors.Load(pod.UID)
	assert.False(t, cached, "the pod's descriptor is dropped")
}

func TestNodeDelete(t *testing.T) {
	r := newTestResolver(t, false)
	node := testNode("node-1", "uid-n1", "172.18.0.2")
	r.onNodeUpsert(node)
	assert.Equal(t, Workload{Name: "node-1", Namespace: "node", Kind: "node"}, r.ResolveIP("172.18.0.2"))

	r.onNodeDelete(cache.DeletedFinalStateUnknown{Obj: node})
	assert.Equal(t, external("172.18.0.2"), r.ResolveIP("172.18.0.2"))
}

func TestDeletedNodeNeverRemovesItsIPsNewOwner(t *testing.T) {
	r := newTestResolver(t, false)
	old := testNode("node-1", "uid-n1", "172.18.0.2")
	r.onNodeUpsert(old)
	r.onNodeUpsert(testNode("node-2", "uid-n2", "172.18.0.2"))

	r.onNodeDelete(old)
	assert.Equal(t, "node-2", r.ResolveIP("172.18.0.2").Name)
}

func TestNodeAddressWinsOverHostNetworkPod(t *testing.T) {
	r := newTestResolver(t, false)
	r.onNodeUpsert(testNode("node-1", "uid-n1", "172.18.0.2"))
	r.onPodUpsert(testPod("kube-proxy", "uid-p", "172.18.0.2", nil))
	assert.Equal(t, "node-1", r.ResolveIP("172.18.0.2").Name)
}

func TestServiceClusterIPs(t *testing.T) {
	r := newTestResolver(t, false)
	r.onServiceUpsert(testService("api", "uid-s", "10.96.0.10", "fd00::10"))
	want := Workload{Name: "api", Namespace: "ns", Kind: "Service"}
	assert.Equal(t, want, r.ResolveIP("10.96.0.10"))
	assert.Equal(t, want, r.ResolveIP("fd00::10"), "every IP family's cluster IP")

	r.onServiceUpsert(testService("api", "uid-s", "10.96.0.11"))
	assert.Equal(t, want, r.ResolveIP("10.96.0.11"), "an updated service resolves at its new IP")

	r.onServiceDelete(testService("api", "uid-s", "10.96.0.10", "10.96.0.11", "fd00::10"))
	assert.Equal(t, external("10.96.0.11"), r.ResolveIP("10.96.0.11"))
	assert.Equal(t, external("fd00::10"), r.ResolveIP("fd00::10"))
}

func TestHeadlessServiceIsIgnored(t *testing.T) {
	r := newTestResolver(t, false)
	r.onServiceUpsert(testService("headless", "uid-h", v1.ClusterIPNone))
	r.onServiceUpsert(testService("pending", "uid-p", ""))
	r.ipsMu.RLock()
	defer r.ipsMu.RUnlock()
	assert.Empty(t, r.ips)
}

func TestServiceNeverDisplacesAnotherObject(t *testing.T) {
	r := newTestResolver(t, false)
	r.onPodUpsert(testPod("web", "uid-p", "10.0.0.1", nil))
	r.onServiceUpsert(testService("svc", "uid-s", "10.0.0.1"))
	assert.Equal(t, "web", r.ResolveIP("10.0.0.1").Name)
}

func TestServiceDeleteFromATombstone(t *testing.T) {
	r := newTestResolver(t, false)
	svc := testService("api", "uid-s", "10.96.0.10")
	r.onServiceUpsert(svc)
	r.onServiceDelete(cache.DeletedFinalStateUnknown{Obj: svc})
	assert.Equal(t, external("10.96.0.10"), r.ResolveIP("10.96.0.10"))
}

func TestUnexpectedObjectsAreIgnored(t *testing.T) {
	r := newTestResolver(t, false)
	assert.NotPanics(t, func() {
		r.onPodUpsert(&v1.Node{})
		r.onPodDelete("not a pod")
		r.onNodeUpsert(&v1.Pod{})
		r.onNodeDelete(cache.DeletedFinalStateUnknown{Obj: &v1.Pod{}})
		r.onServiceUpsert(nil)
		r.onServiceDelete(42)
	})
}

func TestOwnerIsALabelWhenNotTraversing(t *testing.T) {
	r := newTestResolver(t, false)
	r.onPodUpsert(testPod("web-abc", "uid-p", "10.0.0.1", controllerRef("ReplicaSet", "web-rs", "uid-rs")))
	assert.Equal(t, Workload{Name: "web-abc", Namespace: "ns", Kind: "pod", Owner: "web-rs"}, r.ResolveIP("10.0.0.1"))
}

func TestStaticPodResolvesToItsNode(t *testing.T) {
	r := newTestResolver(t, true)
	r.onPodUpsert(testPod("etcd-node-1", "uid-p", "172.18.0.5", controllerRef("Node", "node-1", "uid-n1")))
	assert.Equal(t, Workload{Name: "node-1", Namespace: "ns", Kind: "Node"}, r.ResolveIP("172.18.0.5"))
}

func TestOwnerLookupFailureIsRetriedOnTheNextUpdate(t *testing.T) {
	r := newTestResolver(t, true)
	replicaSets := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	r.replicaSets = appslisters.NewReplicaSetLister(replicaSets)
	pod := testPod("web-abc", "uid-p", "10.0.0.1", controllerRef("ReplicaSet", "web-rs", "uid-rs"))

	r.onPodUpsert(pod)
	assert.Equal(t, Workload{Name: "web-rs", Namespace: "ns", Kind: "ReplicaSet"}, r.ResolveIP("10.0.0.1"),
		"while the ReplicaSet isn't in the cache yet, the closest known owner is used")

	assert.NoError(t, replicaSets.Add(&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "web-rs", Namespace: "ns", UID: "uid-rs",
		OwnerReferences: []metav1.OwnerReference{*controllerRef("Rollout", "web", "uid-ro")},
	}}))
	r.onPodUpsert(pod)
	assert.Equal(t, Workload{Name: "web", Namespace: "ns", Kind: "Rollout"}, r.ResolveIP("10.0.0.1"),
		"the failed lookup wasn't cached, so the pod's next update resolves further up")
}

func TestOwnerWithADifferentUIDIsNotFollowed(t *testing.T) {
	_, err := controllerOf(&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "web-rs", UID: "uid-new"}}, nil,
		controllerRef("ReplicaSet", "web-rs", "uid-old"))
	assert.ErrorContains(t, err, "expected uid-old", "a recreated owner with the same name isn't the pod's owner")

	_, err = controllerOf[*appsv1.ReplicaSet](nil, errors.New("not found"), controllerRef("ReplicaSet", "web-rs", "uid-rs"))
	assert.ErrorContains(t, err, "missing ReplicaSet")
}

func TestStopWatchingTwiceIsSafe(t *testing.T) {
	r := newTestResolver(t, false)
	assert.NotPanics(t, func() {
		r.StopWatching()
		r.StopWatching()
	})
}

func TestTrimForCacheKeepsOnlyWhatTheResolverReads(t *testing.T) {
	pod := testPod("web", "uid-p", "10.0.0.1", controllerRef("ReplicaSet", "web-rs", "uid-rs"))
	pod.Annotations = map[string]string{"big": "annotation"}
	pod.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}
	pod.Spec.Containers = []v1.Container{{Name: "web", Image: "nginx"}}
	pod.Status.Phase = v1.PodRunning
	pod.Status.Conditions = []v1.PodCondition{{Type: v1.PodReady}}

	obj, err := trimForCache(pod)
	assert.NoError(t, err)
	trimmed := obj.(*v1.Pod)
	assert.Nil(t, trimmed.Annotations)
	assert.Nil(t, trimmed.ManagedFields)
	assert.Empty(t, trimmed.Spec.Containers)
	assert.Empty(t, trimmed.Status.Conditions)
	assert.Equal(t, v1.PodRunning, trimmed.Status.Phase)
	assert.Equal(t, []v1.PodIP{{IP: "10.0.0.1"}}, trimmed.Status.PodIPs)
	assert.Len(t, trimmed.OwnerReferences, 1, "owners are needed to resolve controllers")
	assert.Equal(t, types.UID("uid-p"), trimmed.UID, "the UID guards IP ownership")

	svc, _ := trimForCache(&v1.Service{Spec: v1.ServiceSpec{ClusterIPs: []string{"10.96.0.10"}, Selector: map[string]string{"app": "web"}}})
	assert.Equal(t, v1.ServiceSpec{ClusterIPs: []string{"10.96.0.10"}}, svc.(*v1.Service).Spec)

	node, _ := trimForCache(testNode("node-1", "uid-n", "172.18.0.2"))
	assert.Equal(t, []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: "172.18.0.2"}}, node.(*v1.Node).Status.Addresses)
}
