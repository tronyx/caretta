package tracing

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

type Probes struct {
	Kprobe     link.Link
	Tracepoint link.Link
	BpfObjs    bpfObjects
}

// maxConnections sizes both connection maps; the kernel reserves their memory up front.
func LoadProbes(maxConnections uint32) (Probes, *ebpf.Map, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return Probes{}, nil, fmt.Errorf("error removing memory lock - %v", err)
	}

	spec, err := loadBpf()
	if err != nil {
		return Probes{}, nil, fmt.Errorf("error loading BPF spec: %v", err)
	}
	spec.Maps[bpfMapConnections].MaxEntries = maxConnections
	spec.Maps[bpfMapSockInfos].MaxEntries = maxConnections

	objs := bpfObjects{}
	err = spec.LoadAndAssign(&objs, &ebpf.CollectionOptions{})
	if err != nil {
		var ve *ebpf.VerifierError
		if errors.As(err, &ve) {
			slog.Error("The kernel's eBPF verifier rejected caretta's programs", "verifier_log", fmt.Sprintf("%+v", ve))
		}
		return Probes{}, nil, fmt.Errorf("error loading BPF objects from go-side. %v", err)
	}
	slog.Info("BPF objects loaded", "max_connections", maxConnections)

	// attach a kprobe and tracepoint
	kp, err := link.Kprobe("tcp_data_queue", objs.bpfPrograms.HandleTcpDataQueue, nil)
	if err != nil {
		return Probes{}, nil, fmt.Errorf("error attaching kprobe: %v", err)
	}
	slog.Info("Kprobe attached", "function", "tcp_data_queue")

	tp, err := link.Tracepoint("sock", "inet_sock_set_state", objs.bpfPrograms.HandleSockSetState, nil)
	if err != nil {
		return Probes{}, nil, fmt.Errorf("error attaching tracepoint: %v", err)
	}
	slog.Info("Tracepoint attached", "tracepoint", "sock/inet_sock_set_state")

	return Probes{
		Kprobe:     kp,
		Tracepoint: tp,
		BpfObjs:    objs,
	}, objs.Connections, nil
}

func (objs *Probes) UnloadProbes() error {
	// if any close operation fails, will continue to try closing the rest of the struct,
	// and return the first error
	var resultErr error
	resultErr = nil

	err := objs.Kprobe.Close()
	if err != nil {
		resultErr = err
	}
	err = objs.Tracepoint.Close()
	if err != nil && resultErr == nil {
		resultErr = err
	}
	err = objs.BpfObjs.Close()
	if err != nil && resultErr == nil {
		resultErr = err
	}

	return resultErr
}
