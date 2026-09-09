package componenthealth

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kubevirtv1 "kubevirt.io/api/core/v1"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/config"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
	ctlkubevirtv1 "github.com/harvester/harvester/pkg/generated/controllers/kubevirt.io/v1"
	"github.com/harvester/harvester/pkg/util/virtualmachineinstance"
)

const (
	// componentName is the ComponentHealth resource name owned by the main Harvester controller process.
	componentName = "harvester-controller"

	vmiControllerName = "componentHealthVMIController"

	checkVMHostDeviceLiveMigrationUnsupported = "VMHostDeviceLiveMigrationUnsupported"

	// maxAffectedResourceNames caps the number of names recorded per check, per the HEP truncation rule.
	maxAffectedResourceNames = 20
)

type Handler struct {
	componentHealths     ctlharvesterv1.ComponentHealthClient
	componentHealthCache ctlharvesterv1.ComponentHealthCache
	vmiCache             ctlkubevirtv1.VirtualMachineInstanceCache
}

func Register(ctx context.Context, management *config.Management, _ config.Options) error {
	componentHealths := management.HarvesterFactory.Harvesterhci().V1beta1().ComponentHealth()
	vmis := management.VirtFactory.Kubevirt().V1().VirtualMachineInstance()

	h := &Handler{
		componentHealths:     componentHealths,
		componentHealthCache: componentHealths.Cache(),
		vmiCache:             vmis.Cache(),
	}

	vmis.OnChange(ctx, vmiControllerName, h.OnVMIChanged)

	return nil
}

// OnVMIChanged recomputes the harvester-controller ComponentHealth on every VMI change.
func (h *Handler) OnVMIChanged(_ string, vmi *kubevirtv1.VirtualMachineInstance) (*kubevirtv1.VirtualMachineInstance, error) {
	if vmi == nil {
		return nil, nil
	}
	return vmi, h.reconcile()
}

func (h *Handler) reconcile() error {
	vmis, err := h.vmiCache.List(corev1.NamespaceAll, labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list VirtualMachineInstances: %w", err)
	}

	checks := map[string]harvesterv1.CheckResult{}
	if cr := checkVMHostDeviceLiveMigration(vmis); cr != nil {
		checks[checkVMHostDeviceLiveMigrationUnsupported] = *cr
	}

	return h.updateComponentHealth(checks)
}

func checkVMHostDeviceLiveMigration(vmis []*kubevirtv1.VirtualMachineInstance) *harvesterv1.CheckResult {
	names := virtualmachineinstance.GetVMINamesWithHostDevicesOrGPUs(vmis)
	if len(names) == 0 {
		return nil
	}

	displayNames := names
	truncated := false
	if len(displayNames) > maxAffectedResourceNames {
		displayNames = displayNames[:maxAffectedResourceNames]
		truncated = true
	}

	nameMap := make(map[string]harvesterv1.AffectedResourceDetail, len(displayNames))
	for _, name := range displayNames {
		nameMap[name] = harvesterv1.AffectedResourceDetail{}
	}

	return &harvesterv1.CheckResult{
		Severity:      harvesterv1.SeverityWarning,
		Message:       fmt.Sprintf("%d VM(s) cannot be live migrated: host devices or vGPU devices attached", len(names)),
		AffectedCount: len(names),
		Truncated:     truncated,
		AffectedResources: &harvesterv1.AffectedResources{
			APIVersion: kubevirtv1.SchemeGroupVersion.String(),
			Kind:       "VirtualMachine",
			Names:      nameMap,
		},
	}
}

func (h *Handler) updateComponentHealth(checks map[string]harvesterv1.CheckResult) error {
	existing, err := h.componentHealths.Get(componentName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get ComponentHealth %s: %w", componentName, err)
		}
		created, err := h.componentHealths.Create(&harvesterv1.ComponentHealth{
			ObjectMeta: metav1.ObjectMeta{Name: componentName},
			Status: harvesterv1.ComponentHealthStatus{
				LastCheckedAt: metav1.Now(),
				Checks:        checks,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create ComponentHealth %s: %w", componentName, err)
		}
		existing = created
	}

	if reflect.DeepEqual(existing.Status.Checks, checks) {
		return nil
	}

	toUpdate := existing.DeepCopy()
	toUpdate.Status.LastCheckedAt = metav1.Now()
	toUpdate.Status.Checks = checks
	_, err = h.componentHealths.Update(toUpdate)
	return err
}
