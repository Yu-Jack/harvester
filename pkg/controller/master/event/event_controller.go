package event

import (
	v1 "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	corev1 "k8s.io/api/core/v1"

	ctlkubevirtv1 "github.com/harvester/harvester/pkg/generated/controllers/kubevirt.io/v1"
)

type Controller struct {
	vmClient    ctlkubevirtv1.VirtualMachineClient
	vmCache     ctlkubevirtv1.VirtualMachineCache
	eventClient v1.EventController
	handler     KindHandler
}

func (ctr *Controller) SyncEvent(_ string, event *corev1.Event) (*corev1.Event, error) {
	if event == nil {
		return event, nil
	}

	return ctr.handler.Handle(event, event.InvolvedObject.Name, event.InvolvedObject.Namespace)
}
