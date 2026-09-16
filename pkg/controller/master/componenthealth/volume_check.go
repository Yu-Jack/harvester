package componenthealth

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/labels"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/util"
	longhornv1 "github.com/longhorn/longhorn-manager/k8s/pkg/apis/longhorn/v1beta2"
)

const (
	volumeControllerName = "componentHealthLonghornVolumeController"
	volumeKind           = "Volume"
)

type volumeConditionSummary struct {
	names    []string
	messages map[string]struct{}
}

// OnVolumeChanged recomputes the harvester-controller ComponentHealth on every Longhorn Volume change.
func (h *Handler) OnVolumeChanged(_ string, volume *longhornv1.Volume) (*longhornv1.Volume, error) {
	if volume == nil {
		return nil, nil
	}
	return volume, h.reconcileVolumes()
}

func (h *Handler) reconcileVolumes() error {
	volumes, err := h.volumeCache.List(util.LonghornSystemNamespaceName, labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list Longhorn Volumes: %w", err)
	}

	volumeSummariesByReason := map[string]volumeConditionSummary{}
	for _, volume := range volumes {
		for _, condition := range getFalseConditions(volume) {
			logrus.WithFields(logrus.Fields{
				"volume":            volume.Name,
				"condition_type":    condition.Type,
				"condition_reason":  condition.Reason,
				"condition_message": condition.Message,
			}).Debug("found Longhorn Volume false condition")

			summary := volumeSummariesByReason[condition.Reason]
			if summary.messages == nil {
				summary.messages = map[string]struct{}{}
			}
			summary.names = append(summary.names, volume.Name)
			if condition.Message != "" {
				summary.messages[condition.Message] = struct{}{}
			}
			volumeSummariesByReason[condition.Reason] = summary
		}
	}

	checks := map[string]harvesterv1.CheckResult{}
	for reason, summary := range volumeSummariesByReason {
		checks[reason] = buildVolumeCheckResult(harvesterv1.SeverityWarning, reason, summary.names, getSortedMessages(summary.messages))
	}

	return h.updateComponentHealthChecksWithFilter(volumeComponentHealthName, checks, isVolumeCheck)
}

func getFalseConditions(volume *longhornv1.Volume) []longhornv1.Condition {
	conditions := make([]longhornv1.Condition, 0)
	for _, condition := range volume.Status.Conditions {
		if condition.Status == longhornv1.ConditionStatusFalse &&
			condition.Reason != "" {
			conditions = append(conditions, condition)
		}
	}
	return conditions
}

func getSortedMessages(messageSet map[string]struct{}) []string {
	messages := make([]string, 0, len(messageSet))
	for message := range messageSet {
		messages = append(messages, message)
	}
	sort.Strings(messages)
	return messages
}

func isVolumeCheck(_ string, check harvesterv1.CheckResult) bool {
	return check.AffectedResources != nil &&
		check.AffectedResources.APIVersion == longhornv1.SchemeGroupVersion.String() &&
		check.AffectedResources.Kind == volumeKind
}

func buildVolumeCheckResult(severity harvesterv1.Severity, reason string, names []string, messages []string) harvesterv1.CheckResult {
	nameMap := make(map[string]harvesterv1.AffectedResourceDetail, len(names))
	for _, name := range names {
		nameMap[name] = harvesterv1.AffectedResourceDetail{}
	}

	message := fmt.Sprintf("%d Longhorn Volume(s) have %s", len(names), reason)
	if len(messages) > 0 {
		message = fmt.Sprintf("%s: %s", message, strings.Join(messages, "; "))
	}

	return harvesterv1.CheckResult{
		Severity:      severity,
		Message:       message,
		AffectedCount: len(names),
		AffectedResources: &harvesterv1.AffectedResources{
			APIVersion: longhornv1.SchemeGroupVersion.String(),
			Kind:       volumeKind,
			Names:      nameMap,
		},
	}
}
