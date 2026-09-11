package healthsummary

import (
	"context"
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/config"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
)

const (
	summaryName           = "cluster"
	summaryControllerName = "healthSummaryReconciler"
)

type Handler struct {
	healthSummaries     ctlharvesterv1.HealthSummaryClient
	componentHealthCtrl ctlharvesterv1.ComponentHealthClient
}

func Register(ctx context.Context, management *config.Management, _ config.Options) error {
	componentHealthCtrl := management.HarvesterFactory.Harvesterhci().V1beta1().ComponentHealth()
	healthSummaries := management.HarvesterFactory.Harvesterhci().V1beta1().HealthSummary()

	h := &Handler{
		healthSummaries:     healthSummaries,
		componentHealthCtrl: componentHealthCtrl,
	}

	componentHealthCtrl.OnChange(ctx, summaryControllerName, h.onComponentHealthChanged)
	return nil
}

// onComponentHealthChanged fires for every ComponentHealth event and reconciles HealthSummary/cluster.
func (h *Handler) onComponentHealthChanged(_ string, obj *harvesterv1.ComponentHealth) (*harvesterv1.ComponentHealth, error) {
	if obj != nil {
		return obj, nil
	}
	return obj, h.reconcileSummary()
}

func (h *Handler) reconcileSummary() error {
	allHealths, err := h.componentHealthCtrl.List(metav1.ListOptions{
		LabelSelector: labels.Everything().String(),
	})
	if err != nil {
		return fmt.Errorf("failed to list ComponentHealths: %w", err)
	}

	components := map[string]harvesterv1.ComponentSummary{}
	for _, ch := range allHealths.Items {
		key := ch.Name
		if component, ok := ch.Labels[harvesterv1.LabelKeyComponent]; ok && component != "" {
			key = component
		}
		entry := components[key]
		for _, check := range ch.Status.Checks {
			switch check.Severity {
			case harvesterv1.SeverityError:
				entry.ErrorCount++
			case harvesterv1.SeverityWarning:
				entry.WarningCount++
			}
		}
		components[key] = entry
	}

	return h.updateHealthSummary(components)
}

func (h *Handler) updateHealthSummary(components map[string]harvesterv1.ComponentSummary) error {
	existing, err := h.healthSummaries.Get(summaryName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get HealthSummary %s: %w", summaryName, err)
		}
		_, err = h.healthSummaries.Create(&harvesterv1.HealthSummary{
			ObjectMeta: metav1.ObjectMeta{Name: summaryName},
			Status: harvesterv1.HealthSummaryStatus{
				LastCheckedAt: metav1.Now(),
				Components:    components,
			},
		})
		return err
	}

	if reflect.DeepEqual(existing.Status.Components, components) {
		return nil
	}

	toUpdate := existing.DeepCopy()
	toUpdate.Status.LastCheckedAt = metav1.Now()
	toUpdate.Status.Components = components
	_, err = h.healthSummaries.Update(toUpdate)
	return err
}
