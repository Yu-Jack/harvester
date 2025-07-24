package namespace

import (
	"fmt"

	"github.com/rancher/apiserver/pkg/types"
	ctlcorev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/util"
	supportBundleUtil "github.com/harvester/harvester/pkg/util/supportbundle"
)

const (
	queryParamLink        = "link"
	linkTypeSupportBundle = "supportbundle"
	linkTypeTestUpgrade   = "testupgrade"

	// Upgrade state constants
	upgradeStateLabel     = "harvesterhci.io/upgradeState"
	upgradeStateSucceeded = "Succeeded"
	upgradeStateFailed    = "Failed"
)

type Store struct {
	types.Store
	nsCache      ctlcorev1.NamespaceCache
	upgradeCache ctlharvesterv1.UpgradeCache
}

func (s *Store) List(req *types.APIRequest, schema *types.APISchema) (types.APIObjectList, error) {
	linkType := s.getLinkQueryType(req)
	if linkType == "" {
		return s.Store.List(req, schema)
	}

	switch linkType {
	case linkTypeSupportBundle:
		return s.handleSupportBundleRequest()
	case linkTypeTestUpgrade:
		return s.handleTestUpgradeRequest()
	default:
		return s.Store.List(req, schema)
	}
}

// getLinkQueryType extracts and validates the link query parameter
func (s *Store) getLinkQueryType(req *types.APIRequest) string {
	queryValues, exists := req.Query[queryParamLink]
	if !exists || len(queryValues) == 0 {
		return ""
	}
	return queryValues[0]
}

// handleSupportBundleRequest handles the supportbundle link type request
func (s *Store) handleSupportBundleRequest() (types.APIObjectList, error) {
	objects := s.getSupportBundleUsedNamespaceObj()
	return types.APIObjectList{Objects: objects, Count: len(objects)}, nil
}

// handleTestUpgradeRequest handles the testupgrade link type request
func (s *Store) handleTestUpgradeRequest() (types.APIObjectList, error) {
	hasOngoingUpgrade := s.checkOngoingUpgrades()

	// Create a simple API object to represent the upgrade status
	objects := []types.APIObject{
		{
			Type: "namespace",
			ID:   "testupgrade-status",
			Object: map[string]interface{}{
				"hasOngoingUpgrade": hasOngoingUpgrade,
				"namespace":         util.HarvesterSystemNamespaceName,
			},
		},
	}

	return types.APIObjectList{Objects: objects, Count: len(objects)}, nil
}

// checkOngoingUpgrades checks if there are any ongoing upgrades
func (s *Store) checkOngoingUpgrades() bool {
	req, err := labels.NewRequirement(upgradeStateLabel, selection.NotIn, []string{upgradeStateSucceeded, upgradeStateFailed})
	if err != nil {
		logrus.Warnf("Failed to create label requirement for %s: %v", upgradeStateLabel, err)
		return false
	}

	test, err := s.upgradeCache.List(util.HarvesterSystemNamespaceName, labels.NewSelector())
	if err != nil {
		logrus.Warnf("Failed to list upgrades with label %s: %v", upgradeStateLabel, err)
		return false
	}
	if len(test) == 0 {
		logrus.Infof("AAAAA No ongoing upgrades found")
	} else {
		logrus.Infof("BBBB There are ongoing upgrades: %v", test[0].Name)

	}

	upgradesItems, err := s.upgradeCache.List(util.HarvesterSystemNamespaceName, labels.NewSelector().Add(*req))
	if err != nil {
		logrus.Warnf("Failed to list upgrades with label %s: %v", upgradeStateLabel, err)
		return false
	}
	if len(upgradesItems) > 0 {
		logrus.Infof("There are ongoing upgrades: %v", upgradesItems[0].Name)
		return true
	} else {
		logrus.Infof("No ongoing upgrades found")
		return false
	}

	return false
}

func (s *Store) getSupportBundleUsedNamespaceObj() []types.APIObject {
	defaultNamespaces := supportBundleUtil.DefaultNamespaces()
	apiObjs := make([]types.APIObject, 0, len(defaultNamespaces))

	for _, namespaceName := range defaultNamespaces {
		nsObj, err := s.nsCache.Get(namespaceName)
		if err != nil {
			fmt.Printf("Failed to get namespace %s: %v\n", namespaceName, err)
			continue
		}

		apiObjs = append(apiObjs, types.APIObject{
			Type:   "namespace",
			ID:     namespaceName,
			Object: nsObj,
		})
	}

	return apiObjs
}
