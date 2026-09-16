package componenthealth

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	kubevirtv1 "kubevirt.io/api/core/v1"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/util/virtualmachineinstance"
)

const (
	vmiControllerName = "componentHealthVMIController"
)

// vmMigrationHealthChecks maps VM live-migration rules to the ComponentHealth checks they report.
// Rules not relevant to ComponentHealth (e.g. RuleIsMigrating) are intentionally omitted.
var vmMigrationHealthChecks = []struct {
	Rule     virtualmachineinstance.VMMigrationRule
	Severity harvesterv1.Severity
}{
	{
		Rule:     virtualmachineinstance.RuleHasHostDevicesOrGPUs,
		Severity: harvesterv1.SeverityWarning,
	},
	{
		Rule:     virtualmachineinstance.RuleHasNodeSelectorHostname,
		Severity: harvesterv1.SeverityWarning,
	},
}

// OnVMIChanged recomputes the harvester-controller ComponentHealth on every VMI change.
func (h *Handler) OnVMIChanged(_ string, vmi *kubevirtv1.VirtualMachineInstance) (*kubevirtv1.VirtualMachineInstance, error) {
	if vmi == nil {
		return nil, nil
	}
	return vmi, h.reconcileVMI()
}

func (h *Handler) reconcileVMI() error {
	vmis, err := h.vmiCache.List(corev1.NamespaceAll, labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list VirtualMachineInstances: %w", err)
	}

	checks := map[string]harvesterv1.CheckResult{}
	for _, entry := range vmMigrationHealthChecks {
		names := virtualmachineinstance.GetVMINamesMatching(vmis, entry.Rule.Check)
		if len(names) == 0 {
			continue
		}
		checks[entry.Rule.Key] = buildVMCheckResult(entry.Severity, entry.Rule.Message, names)
	}

	return h.updateComponentHealthChecks(vmComponentHealthName, checks, vmiCheckKeys())
}

func vmiCheckKeys() []string {
	keys := make([]string, 0, len(vmMigrationHealthChecks))
	for _, entry := range vmMigrationHealthChecks {
		keys = append(keys, entry.Rule.Key)
	}
	return keys
}

func buildVMCheckResult(severity harvesterv1.Severity, reason string, names []string) harvesterv1.CheckResult {
	nameMap := make(map[string]harvesterv1.AffectedResourceDetail, len(names))
	for _, name := range names {
		nameMap[name] = harvesterv1.AffectedResourceDetail{}
	}

	return harvesterv1.CheckResult{
		Severity:      severity,
		Message:       fmt.Sprintf("%d VM(s) cannot be live migrated: %s", len(names), reason),
		AffectedCount: len(names),
		AffectedResources: &harvesterv1.AffectedResources{
			APIVersion: kubevirtv1.SchemeGroupVersion.String(),
			Kind:       kubevirtv1.VirtualMachineGroupVersionKind.Kind,
			Names:      nameMap,
		},
	}
}
