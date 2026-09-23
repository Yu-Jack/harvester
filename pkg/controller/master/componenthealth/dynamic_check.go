package componenthealth

import (
	"fmt"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
)

const (
	dynamicFieldsConfigMapName = "componenthealth-dynamic-fields"
	dynamicFieldsDataKey       = "rules.yaml"
)

type dynamicFieldRule struct {
	Name                string               `json:"name"`
	ComponentHealthName string               `json:"componentHealthName"`
	Resource            dynamicFieldResource `json:"resource"`
	FieldPath           string               `json:"fieldPath"`
	MatchValue          string               `json:"matchValue"`
	Severity            harvesterv1.Severity `json:"severity"`
	Message             string               `json:"message"`
}

type dynamicFieldResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

func (h *Handler) dynamicChecks(componentHealthName string, gvk schema.GroupVersionKind, objects []runtime.Object) (map[string]harvesterv1.CheckResult, []string, error) {
	rules, err := h.dynamicFieldRules()
	if err != nil {
		return nil, nil, err
	}

	checks := make(map[string]harvesterv1.CheckResult)
	ownedKeys := make([]string, 0)
	for _, rule := range rules {
		if rule.ComponentHealthName != componentHealthName || rule.Resource.APIVersion != gvk.GroupVersion().String() || rule.Resource.Kind != gvk.Kind {
			continue
		}

		ownedKeys = append(ownedKeys, rule.Name)
		matchedNames := make([]string, 0)
		for _, object := range objects {
			matched, err := dynamicFieldMatches(object, rule.FieldPath, rule.MatchValue)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid dynamic field rule %q: %w", rule.Name, err)
			}
			if matched {
				accessor, ok := object.(metav1.Object)
				if !ok {
					return nil, nil, fmt.Errorf("resource for dynamic field rule %q does not expose metadata", rule.Name)
				}
				matchedNames = append(matchedNames, accessor.GetName())
			}
		}
		if len(matchedNames) > 0 {
			checks[rule.Name] = buildDynamicCheckResult(rule, gvk, matchedNames)
		}
	}

	return checks, ownedKeys, nil
}

func (h *Handler) dynamicFieldRules() ([]dynamicFieldRule, error) {
	if h.configMapCache == nil {
		return nil, nil
	}
	configMap, err := h.configMapCache.Get(dynamicFieldsNamespace, dynamicFieldsConfigMapName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get dynamic component health ConfigMap: %w", err)
	}
	contents, ok := configMap.Data[dynamicFieldsDataKey]
	if !ok || strings.TrimSpace(contents) == "" {
		return nil, nil
	}

	var rules []dynamicFieldRule
	if err := yaml.UnmarshalStrict([]byte(contents), &rules); err != nil {
		return nil, fmt.Errorf("failed to parse dynamic component health rules: %w", err)
	}
	names := make(map[string]struct{}, len(rules))
	for index := range rules {
		if err := validateDynamicFieldRule(rules[index]); err != nil {
			return nil, fmt.Errorf("invalid dynamic component health rule at index %d: %w", index, err)
		}
		if _, ok := names[rules[index].Name]; ok {
			return nil, fmt.Errorf("duplicate rule name %q", rules[index].Name)
		}
		names[rules[index].Name] = struct{}{}
	}
	return rules, nil
}

func dynamicObjects[T runtime.Object](objects []T) []runtime.Object {
	result := make([]runtime.Object, 0, len(objects))
	for _, object := range objects {
		result = append(result, object)
	}
	return result
}

func validateDynamicFieldRule(rule dynamicFieldRule) error {
	if rule.Name == "" || rule.ComponentHealthName == "" || rule.Message == "" {
		return fmt.Errorf("name, componentHealthName, and message are required")
	}
	if rule.Severity != harvesterv1.SeverityError && rule.Severity != harvesterv1.SeverityWarning && rule.Severity != harvesterv1.SeverityInfo {
		return fmt.Errorf("unsupported severity %q", rule.Severity)
	}
	if !supportedDynamicResource(rule.Resource) {
		return fmt.Errorf("resource %s/%s is not supported", rule.Resource.APIVersion, rule.Resource.Kind)
	}
	path := strings.Split(rule.FieldPath, ".")
	if len(path) < 2 || (path[0] != "spec" && path[0] != "status") {
		return fmt.Errorf("fieldPath must start with spec or status and contain a field")
	}
	for _, segment := range path {
		if segment == "" {
			return fmt.Errorf("fieldPath contains an empty segment")
		}
	}
	return nil
}

func supportedDynamicResource(resource dynamicFieldResource) bool {
	supported := map[dynamicFieldResource]struct{}{
		{APIVersion: "v1", Kind: "Node"}:                                      {},
		{APIVersion: "kubevirt.io/v1", Kind: "VirtualMachineInstance"}:        {},
		{APIVersion: "longhorn.io/v1beta2", Kind: "Volume"}:                   {},
		{APIVersion: "harvesterhci.io/v1beta1", Kind: "VirtualMachineBackup"}: {},
		{APIVersion: "harvesterhci.io/v1beta1", Kind: "ScheduleVMBackup"}:     {},
	}
	_, ok := supported[resource]
	return ok
}

func dynamicFieldMatches(object runtime.Object, fieldPath, matchValue string) (bool, error) {
	unstructuredObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		return false, err
	}
	var value interface{} = unstructuredObject
	for _, segment := range strings.Split(fieldPath, ".") {
		object, ok := value.(map[string]interface{})
		if !ok {
			return false, fmt.Errorf("fieldPath %q traverses a non-object value", fieldPath)
		}
		value, ok = object[segment]
		if !ok {
			return false, nil
		}
	}
	valueKind := reflect.ValueOf(value).Kind()
	switch valueKind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprint(value) == matchValue, nil
	default:
		return false, fmt.Errorf("fieldPath %q does not resolve to a scalar", fieldPath)
	}
}

func buildDynamicCheckResult(rule dynamicFieldRule, gvk schema.GroupVersionKind, names []string) harvesterv1.CheckResult {
	nameMap := make(map[string]harvesterv1.AffectedResourceDetail, len(names))
	for _, name := range names {
		nameMap[name] = harvesterv1.AffectedResourceDetail{}
	}
	return harvesterv1.CheckResult{
		Severity:      rule.Severity,
		Message:       rule.Message,
		AffectedCount: len(names),
		AffectedResources: &harvesterv1.AffectedResources{
			APIVersion: gvk.GroupVersion().String(),
			Kind:       gvk.Kind,
			Names:      nameMap,
		},
	}
}
