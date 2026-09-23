package componenthealth

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
)

const (
	vmBackupControllerName      = "componentHealthVMBackupController"
	vmBackupComponentHealthName = "harvester-controller-vmbackup"
	vmBackupKind                = "VirtualMachineBackup"

	// checkKeyVMBackupFailed is the ComponentHealth check key reported when a VirtualMachineBackup's
	// latest VolumeBackup fails.
	checkKeyVMBackupFailed = "VMBackupFailed"
)

// OnVMBackupChanged recomputes the harvester-controller ComponentHealth on every VirtualMachineBackup change.
func (h *Handler) OnVMBackupChanged(_ string, vmBackup *harvesterv1.VirtualMachineBackup) (*harvesterv1.VirtualMachineBackup, error) {
	if vmBackup == nil {
		return nil, nil
	}
	return vmBackup, h.reconcileVMBackups()
}

func (h *Handler) reconcileVMBackups() error {
	vmBackups, err := h.vmBackupCache.List(corev1.NamespaceAll, labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list VirtualMachineBackups: %w", err)
	}

	var names []string
	messageSet := map[string]struct{}{}
	for _, vmBackup := range vmBackups {
		volumeBackup := getLatestVolumeBackupWithError(vmBackup)
		if volumeBackup == nil {
			continue
		}
		names = append(names, vmBackup.Name)
		messageSet[*volumeBackup.Error.Message] = struct{}{}
	}

	checks := map[string]harvesterv1.CheckResult{}
	if len(names) > 0 {
		checks[checkKeyVMBackupFailed] = buildVMBackupCheckResult(names, getSortedMessages(messageSet))
	}

	return h.updateComponentHealthChecksWithDynamic(vmBackupComponentHealthName, checks, func(key string, _ harvesterv1.CheckResult) bool {
		return key == checkKeyVMBackupFailed
	}, harvesterv1.SchemeGroupVersion.WithKind(vmBackupKind), dynamicObjects(vmBackups))
}

// getLatestVolumeBackupWithError returns the most recently created VolumeBackup that has an
// error, or nil if the VirtualMachineBackup has no failed VolumeBackup.
func getLatestVolumeBackupWithError(vmBackup *harvesterv1.VirtualMachineBackup) *harvesterv1.VolumeBackup {
	var latest *harvesterv1.VolumeBackup
	for i := range vmBackup.Status.VolumeBackups {
		volumeBackup := &vmBackup.Status.VolumeBackups[i]
		if volumeBackup.Error == nil || volumeBackup.Error.Message == nil {
			continue
		}
		if latest == nil || volumeBackupCreatedAfter(volumeBackup, latest) {
			latest = volumeBackup
		}
	}
	return latest
}

func volumeBackupCreatedAfter(a, b *harvesterv1.VolumeBackup) bool {
	if a.CreationTime == nil {
		return false
	}
	if b.CreationTime == nil {
		return true
	}
	return a.CreationTime.After(b.CreationTime.Time)
}

func buildVMBackupCheckResult(names []string, messages []string) harvesterv1.CheckResult {
	nameMap := make(map[string]harvesterv1.AffectedResourceDetail, len(names))
	for _, name := range names {
		nameMap[name] = harvesterv1.AffectedResourceDetail{}
	}

	message := fmt.Sprintf("%d VirtualMachineBackup(s) failed", len(names))
	if len(messages) > 0 {
		message = fmt.Sprintf("%s: %s", message, strings.Join(messages, "; "))
	}

	return harvesterv1.CheckResult{
		Severity:      harvesterv1.SeverityError,
		Message:       message,
		AffectedCount: len(names),
		AffectedResources: &harvesterv1.AffectedResources{
			APIVersion: harvesterv1.SchemeGroupVersion.String(),
			Kind:       vmBackupKind,
			Names:      nameMap,
		},
	}
}
