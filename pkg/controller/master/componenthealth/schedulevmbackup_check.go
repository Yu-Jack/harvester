package componenthealth

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
)

const (
	scheduleVMBackupControllerName      = "componentHealthScheduleVMBackupController"
	scheduleVMBackupComponentHealthName = "harvester-controller-schedulevmbackup"
	scheduleVMBackupKind                = "ScheduleVMBackup"
)

// OnScheduleVMBackupChanged recomputes the harvester-controller ComponentHealth on every ScheduleVMBackup change.
func (h *Handler) OnScheduleVMBackupChanged(_ string, scheduleVMBackup *harvesterv1.ScheduleVMBackup) (*harvesterv1.ScheduleVMBackup, error) {
	if scheduleVMBackup == nil {
		return nil, nil
	}
	return scheduleVMBackup, h.reconcileScheduleVMBackups()
}

func (h *Handler) reconcileScheduleVMBackups() error {
	scheduleVMBackups, err := h.scheduleVMBackupCache.List(corev1.NamespaceAll, labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list ScheduleVMBackups: %w", err)
	}

	summariesByReason := map[string]volumeConditionSummary{}
	for _, scheduleVMBackup := range scheduleVMBackups {
		for _, condition := range getTrueConditions(scheduleVMBackup) {
			summary := summariesByReason[condition.Reason]
			if summary.messages == nil {
				summary.messages = map[string]struct{}{}
			}
			summary.names = append(summary.names, scheduleVMBackup.Name)
			if condition.Message != "" {
				summary.messages[condition.Message] = struct{}{}
			}
			summariesByReason[condition.Reason] = summary
		}
	}

	checks := map[string]harvesterv1.CheckResult{}
	for reason, summary := range summariesByReason {
		checks[reason] = buildScheduleVMBackupCheckResult(reason, summary.names, getSortedMessages(summary.messages))
	}

	return h.updateComponentHealthChecksWithFilter(scheduleVMBackupComponentHealthName, checks, isScheduleVMBackupCheck)
}

// getTrueConditions returns the ScheduleVMBackup conditions reporting a problem, i.e. status True with a reason.
func getTrueConditions(scheduleVMBackup *harvesterv1.ScheduleVMBackup) []harvesterv1.Condition {
	conditions := make([]harvesterv1.Condition, 0)
	for _, condition := range scheduleVMBackup.Status.Conditions {
		if condition.Status == corev1.ConditionTrue && condition.Reason != "" {
			conditions = append(conditions, condition)
		}
	}
	return conditions
}

func isScheduleVMBackupCheck(_ string, check harvesterv1.CheckResult) bool {
	return check.AffectedResources != nil &&
		check.AffectedResources.APIVersion == harvesterv1.SchemeGroupVersion.String() &&
		check.AffectedResources.Kind == scheduleVMBackupKind
}

func buildScheduleVMBackupCheckResult(reason string, names []string, messages []string) harvesterv1.CheckResult {
	nameMap := make(map[string]harvesterv1.AffectedResourceDetail, len(names))
	for _, name := range names {
		nameMap[name] = harvesterv1.AffectedResourceDetail{}
	}

	message := fmt.Sprintf("%d ScheduleVMBackup(s) have %s", len(names), reason)
	if len(messages) > 0 {
		message = fmt.Sprintf("%s: %s", message, strings.Join(messages, "; "))
	}

	return harvesterv1.CheckResult{
		Severity:      harvesterv1.SeverityError,
		Message:       message,
		AffectedCount: len(names),
		AffectedResources: &harvesterv1.AffectedResources{
			APIVersion: harvesterv1.SchemeGroupVersion.String(),
			Kind:       scheduleVMBackupKind,
			Names:      nameMap,
		},
	}
}
