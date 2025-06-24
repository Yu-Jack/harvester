package namespace

import (
	"fmt"

	"github.com/rancher/apiserver/pkg/types"
	ctlcorev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"

	supportBundleUtil "github.com/harvester/harvester/pkg/util/supportbundle"
)

type Store struct {
	types.Store
	nsCache ctlcorev1.NamespaceCache
}

func (s *Store) List(req *types.APIRequest, schema *types.APISchema) (types.APIObjectList, error) {
	fmt.Println("Namespace Store List called")

	queryType, ok := req.Query["link"]
	if !ok || len(queryType) == 0 {
		return s.Store.List(req, schema)
	}

	switch queryType[0] {
	case "supportbundle":
		objects := s.getSupportBundleUsedNamespaceObj()
		return types.APIObjectList{Objects: objects, Count: len(objects)}, nil
	default:
		return s.Store.List(req, schema)
	}
}

func (s *Store) getSupportBundleUsedNamespaceObj() []types.APIObject {
	var objects []types.APIObject

	for _, namespaceName := range supportBundleUtil.DefaultNamespaces() {
		nsObj, err := s.nsCache.Get(namespaceName)
		if err != nil {
			fmt.Printf("Failed to get namespace %s: %v\n", namespaceName, err)
			continue
		}

		objects = append(objects, types.APIObject{
			Type:   "namespace",
			ID:     namespaceName,
			Object: nsObj,
		})
	}

	return objects
}
