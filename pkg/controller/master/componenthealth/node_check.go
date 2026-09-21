package componenthealth

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
)

const (
	nodeControllerName   = "componentHealthNodeController"
	checkKeyNodeCordoned = "NodeCordoned"
	nodeKind             = "Node"
)

func (h *Handler) OnNodeChanged(_ string, node *corev1.Node) (*corev1.Node, error) {
	if node == nil {
		return nil, nil
	}
	return node, h.reconcileNodes()
}

func (h *Handler) reconcileNodes() error {
	nodes, err := h.nodeCache.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list Nodes: %w", err)
	}

	cordonedNodeNames := make([]string, 0)
	for _, node := range nodes {
		if node.Spec.Unschedulable {
			cordonedNodeNames = append(cordonedNodeNames, node.Name)
		}
	}

	checks := map[string]harvesterv1.CheckResult{}
	if len(cordonedNodeNames) > 0 {
		checks[checkKeyNodeCordoned] = buildNodeCordonedCheckResult(cordonedNodeNames)
	}

	return h.updateComponentHealthChecks(nodeComponentHealthName, checks, []string{checkKeyNodeCordoned})
}

func buildNodeCordonedCheckResult(names []string) harvesterv1.CheckResult {
	nameMap := make(map[string]harvesterv1.AffectedResourceDetail, len(names))
	for _, name := range names {
		nameMap[name] = harvesterv1.AffectedResourceDetail{}
	}

	return harvesterv1.CheckResult{
		Severity:      harvesterv1.SeverityInfo,
		Message:       fmt.Sprintf("%d Node(s) are cordoned", len(names)),
		AffectedCount: len(names),
		AffectedResources: &harvesterv1.AffectedResources{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       nodeKind,
			Names:      nameMap,
		},
	}
}
