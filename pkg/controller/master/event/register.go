package event

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	"github.com/harvester/harvester/pkg/config"
)

const (
	eventControllerSyncEvent = "EventController.SyncEvent"
)

type KindHandler interface {
	Match(kind string) bool
	Handle(event *corev1.Event, resourceName, resourceNamespace string) (*corev1.Event, error)
}

func Register(ctx context.Context, management *config.Management, options config.Options) error {
	var (
		vmClient    = management.VirtFactory.Kubevirt().V1().VirtualMachine()
		vmCache     = management.VirtFactory.Kubevirt().V1().VirtualMachine().Cache()
		eventClient = management.CoreFactory.Core().V1().Event()
	)

	eventCtrl := &Controller{
		vmClient:    vmClient,
		vmCache:     vmCache,
		eventClient: eventClient,
	}

	kindHandler := NewHandler(
		NewVmiHandler(eventCtrl), nil,
	)

	eventCtrl.handler = kindHandler

	eClient := management.CoreFactory.Core().V1().Event()
	eClient.OnChange(ctx, eventControllerSyncEvent, eventCtrl.SyncEvent)

	return nil
}
