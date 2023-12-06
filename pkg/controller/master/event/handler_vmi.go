package event

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"

	"github.com/harvester/harvester/pkg/util"
)

type vmiHandler struct {
	ctr *Controller
}

func NewVmiHandler(ctrl *Controller) KindHandler {
	return &vmiHandler{
		ctr: ctrl,
	}
}

func (v *vmiHandler) Match(kind string) bool {
	fmt.Println("kind: ", kind)
	return kind == "VirtualMachineInstance"
}

func (v *vmiHandler) Handle(event *corev1.Event, resourceName, resourceNamespace string) (*corev1.Event, error) {
	var (
		oldEvents       []*corev1.Event
		newEvents       []*corev1.Event
		eventRecords    string
		newEventRecords []byte
		latestEvents    []*corev1.Event
		keepLatestCount = 5
		err             error
	)

	vm, err := v.ctr.vmCache.Get(resourceNamespace, resourceName)
	if err != nil {
		logrus.Errorf("failed to get vm: %v", err)
		return event, nil
	}
	vmDp := vm.DeepCopy()

	eventRecords, _ = vmDp.Annotations[util.AnnotationVMIEventRecords]

	if eventRecords != "" {
		if err = json.Unmarshal([]byte(eventRecords), &oldEvents); err != nil {
			logrus.Errorf("updateVMEventRecord: failed to unmarshal eventRecords: %v", err)
			return event, err
		}
	}

	// When we append new events, we should trim it by keepLatestCount.
	// Make sure we don't put too many events in the annotation.
	newEvents = append(oldEvents, event)

	if len(newEvents) > keepLatestCount {
		latestEvents = newEvents[len(newEvents)-keepLatestCount:]
	} else {
		latestEvents = newEvents
	}

	if newEventRecords, err = json.Marshal(latestEvents); err != nil {
		logrus.Errorf("updateVMEventRecord: failed to marshal latestEvents: %v", err)
		return event, err
	}

	vmDp.Annotations[util.AnnotationVMIEventRecords] = string(newEventRecords)

	if _, err := v.ctr.vmClient.Update(vmDp); err != nil {
		logrus.Errorf("updateVMEventRecord: failed to update vm: %v", err)
		return event, err
	}

	return event, nil
}
