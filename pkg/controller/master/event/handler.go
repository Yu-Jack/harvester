package event

import (
	corev1 "k8s.io/api/core/v1"
)

type kind struct {
	handler KindHandler
	next    KindHandler
}

func (k *kind) Match(kind string) bool {
	// ignore
	return false
}

func (k *kind) Handle(event *corev1.Event, resourceName, resourceNamespace string) (*corev1.Event, error) {
	if k.handler.Match(event.InvolvedObject.Kind) {
		return k.handler.Handle(event, resourceName, resourceNamespace)
	}

	if k.next != nil {
		return k.next.Handle(event, resourceName, resourceNamespace)
	}

	return event, nil
}

func NewHandler(handler, next KindHandler) KindHandler {
	return &kind{
		handler: handler,
		next:    next,
	}
}
