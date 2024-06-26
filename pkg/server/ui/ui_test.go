package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_HandlerPath(t *testing.T) {
	invalidPage := "https://harvesterhci.io.not-existing/index.html"
	validPage := "https://harvesterhci.io/index.html"
	exampleDir := "/example"

	tests := []struct {
		name          string
		uiIndex       string
		uiPath        string
		uiSource      string
		expectedPath  string
		expectedIsURL bool
	}{
		{
			name:          "Test Case 1: ui source setting is auto, URL can be downloaded",
			uiIndex:       validPage,
			uiPath:        exampleDir,
			uiSource:      uiSourceAuto,
			expectedPath:  validPage,
			expectedIsURL: true,
		},
		{
			name:          "Test Case 2: ui source setting is auto, URL cannot be downloaded",
			uiIndex:       invalidPage,
			uiPath:        exampleDir,
			uiSource:      uiSourceAuto,
			expectedPath:  exampleDir,
			expectedIsURL: false,
		},
		{
			name:          "Test Case 3: ui source setting is bundled, URL can be downloaded",
			uiIndex:       validPage,
			uiPath:        exampleDir,
			uiSource:      uiSourceBundled,
			expectedPath:  exampleDir,
			expectedIsURL: false,
		},
		{
			name:          "Test Case 4: ui source setting is invalid string, URL can be downloaded",
			uiIndex:       validPage,
			uiPath:        exampleDir,
			uiSource:      "invalid-string",
			expectedPath:  validPage,
			expectedIsURL: true,
		},
		{
			name:          "Test Case 5: ui source setting is invalid string, URL cannot be downloaded",
			uiIndex:       invalidPage,
			uiPath:        exampleDir,
			uiSource:      "invalid-string",
			expectedPath:  invalidPage,
			expectedIsURL: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &handler{
				offlineSetting: func() string { return tt.uiSource },
				pathSetting:    func() string { return tt.uiPath },
				indexSetting:   func() string { return tt.uiIndex },
			}

			path, isURL := h.path()

			assert.Equal(t, path, tt.expectedPath, tt.name)
			assert.Equal(t, isURL, tt.expectedIsURL, tt.name)
		})
	}
}

func Test_HandlerPathMultipleCalledInAutoMode(t *testing.T) {
	invalidPage := "https://harvesterhci.io.not-existing/index.html"
	validPage := "https://harvesterhci.io/index.html"
	exampleDir := "/example"

	page := invalidPage

	h := &handler{
		offlineSetting: func() string { return uiSourceAuto },
		pathSetting:    func() string { return exampleDir },
		indexSetting:   func() string { return page },
	}

	path, isURL := h.path()
	assert.Equal(t, path, exampleDir)
	assert.Equal(t, isURL, false)

	page = validPage
	path, isURL = h.path()
	assert.Equal(t, path, validPage)
	assert.Equal(t, isURL, true)
}
