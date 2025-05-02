package main

import (
	v3bindata "github.com/go-bindata/go-bindata/v3"
)

var CRDPaths = []v3bindata.InputConfig{
	{
		Path: "./deploy/charts/harvester/dependency_charts/cdi/crds/",
	},
	{
		Path: "./deploy/charts/harvester/dependency_charts/csi-snapshotter/crds/",
	},
	{
		Path: "./deploy/charts/harvester/dependency_charts/kubevirt-operator/crds/",
	},
	{
		Path: "./deploy/charts/harvester/dependency_charts/whereabouts/crds/",
	},
}

func main() {

	c := &v3bindata.Config{
		Input:   CRDPaths,
		Output:  "./pkg/data/data.go",
		Package: "data",
	}

	err := v3bindata.Translate(c)
	if err != nil {
		panic(err)
	}
}
