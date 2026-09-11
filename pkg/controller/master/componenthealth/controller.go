package componenthealth

import (
	"context"
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/config"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
	ctlkubevirtv1 "github.com/harvester/harvester/pkg/generated/controllers/kubevirt.io/v1"
)

// componentName is the ComponentHealth resource name owned by the main Harvester controller process.
const componentName = "harvester-controller"

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

func (h *Handler) updateComponentHealth(checks map[string]harvesterv1.CheckResult) error {
	existing, err := h.componentHealths.Get(componentName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get ComponentHealth %s: %w", componentName, err)
		}
		created, err := h.componentHealths.Create(&harvesterv1.ComponentHealth{
			ObjectMeta: metav1.ObjectMeta{
				Name: componentName,
				Labels: map[string]string{
					harvesterv1.LabelKeyComponent: componentName,
				},
			},
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
	if toUpdate.Labels == nil {
		toUpdate.Labels = map[string]string{}
	}
	toUpdate.Labels[harvesterv1.LabelKeyComponent] = componentName
	toUpdate.Status.LastCheckedAt = metav1.Now()
	toUpdate.Status.Checks = checks
	_, err = h.componentHealths.Update(toUpdate)
	return err
}
