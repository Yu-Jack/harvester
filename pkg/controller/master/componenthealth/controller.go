package componenthealth

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/config"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
	ctlkubevirtv1 "github.com/harvester/harvester/pkg/generated/controllers/kubevirt.io/v1"
	ctllonghornv1 "github.com/harvester/harvester/pkg/generated/controllers/longhorn.io/v1beta2"
)

const (
	// componentName is the logical component name owned by the main Harvester controller process.
	componentName             = "harvester-controller"
	vmComponentHealthName     = "harvester-controller-vm"
	volumeComponentHealthName = "harvester-controller-volume"
)

type Handler struct {
	componentHealths     ctlharvesterv1.ComponentHealthClient
	componentHealthCache ctlharvesterv1.ComponentHealthCache
	vmiCache             ctlkubevirtv1.VirtualMachineInstanceCache
	volumeCache          ctllonghornv1.VolumeCache
}

func Register(ctx context.Context, management *config.Management, _ config.Options) error {
	componentHealths := management.HarvesterFactory.Harvesterhci().V1beta1().ComponentHealth()
	vmis := management.VirtFactory.Kubevirt().V1().VirtualMachineInstance()
	volumes := management.LonghornFactory.Longhorn().V1beta2().Volume()

	h := &Handler{
		componentHealths:     componentHealths,
		componentHealthCache: componentHealths.Cache(),
		vmiCache:             vmis.Cache(),
		volumeCache:          volumes.Cache(),
	}

	vmis.OnChange(ctx, vmiControllerName, h.OnVMIChanged)
	volumes.OnChange(ctx, volumeControllerName, h.OnVolumeChanged)

	return nil
}

func (h *Handler) updateComponentHealthChecks(componentHealthName string, checks map[string]harvesterv1.CheckResult, ownedKeys []string) error {
	ownedKeySet := make(map[string]struct{}, len(ownedKeys))
	for _, key := range ownedKeys {
		ownedKeySet[key] = struct{}{}
	}
	return h.updateComponentHealthChecksWithFilter(componentHealthName, checks, func(key string, _ harvesterv1.CheckResult) bool {
		_, ok := ownedKeySet[key]
		return ok
	})
}

func (h *Handler) updateComponentHealthChecksWithFilter(componentHealthName string, checks map[string]harvesterv1.CheckResult, ownedCheck func(string, harvesterv1.CheckResult) bool) error {
	existing, err := h.componentHealths.Get(componentHealthName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get ComponentHealth %s: %w", componentHealthName, err)
		}
		created, err := h.componentHealths.Create(&harvesterv1.ComponentHealth{
			ObjectMeta: metav1.ObjectMeta{
				Name: componentHealthName,
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
			return fmt.Errorf("failed to create ComponentHealth %s: %w", componentHealthName, err)
		}
		logrus.WithFields(logrus.Fields{
			"component":        componentName,
			"component_health": componentHealthName,
			"checks":           getCheckKeys(checks),
		}).Info("created ComponentHealth checks")
		existing = created
	}

	updatedChecks := make(map[string]harvesterv1.CheckResult, len(existing.Status.Checks)+len(checks))
	for key, check := range existing.Status.Checks {
		updatedChecks[key] = check
	}
	for key, check := range existing.Status.Checks {
		if ownedCheck(key, check) {
			delete(updatedChecks, key)
		}
	}
	for key, check := range checks {
		updatedChecks[key] = check
	}

	if reflect.DeepEqual(existing.Status.Checks, updatedChecks) {
		logrus.WithFields(logrus.Fields{
			"component":        componentName,
			"component_health": componentHealthName,
			"checks":           getCheckKeys(checks),
		}).Debug("ComponentHealth checks unchanged")
		return nil
	}

	toUpdate := existing.DeepCopy()
	if toUpdate.Labels == nil {
		toUpdate.Labels = map[string]string{}
	}
	toUpdate.Labels[harvesterv1.LabelKeyComponent] = componentName
	toUpdate.Status.LastCheckedAt = metav1.Now()
	toUpdate.Status.Checks = updatedChecks
	_, err = h.componentHealths.Update(toUpdate)
	if err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{
		"component":        componentName,
		"component_health": componentHealthName,
		"checks":           getCheckKeys(checks),
	}).Info("updated ComponentHealth checks")
	return nil
}

func getCheckKeys(checks map[string]harvesterv1.CheckResult) []string {
	keys := make([]string, 0, len(checks))
	for key := range checks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
