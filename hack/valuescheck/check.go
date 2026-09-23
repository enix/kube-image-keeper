package main

import (
	"fmt"
	"maps"
	"slices"

	"sigs.k8s.io/yaml"
)

// processes are the blocks of values.yaml that describe one process each. Every pod
// setting at the root of the file must be repeated in each of them (notes/0008).
var processes = []string{"webhook", "reconciler", "secretSyncer"}

// chartWide are the root keys that are not pod settings, so the process blocks do not
// repeat them. A new root key that is neither a pod setting nor listed here fails the
// check on purpose: it has to be filed on one side or the other.
var chartWide = []string{"rbac", "serviceAccount", "nameOverride", "fullnameOverride"}

// check reports every root pod setting that a process block does not repeat.
func check(values []byte) ([]string, error) {
	var root map[string]any
	if err := yaml.Unmarshal(values, &root); err != nil {
		return nil, fmt.Errorf("parsing the values: %w", err)
	}

	var problems []string
	for _, process := range processes {
		block, ok := root[process].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: the process block is missing", process))
			continue
		}
		for _, key := range rootPodSettings(root) {
			problems = append(problems, missing(process, key, root[key], block, key)...)
		}
	}

	return problems, nil
}

// rootPodSettings lists the root keys that apply to every process, sorted for a stable output.
func rootPodSettings(root map[string]any) []string {
	var keys []string
	for key := range root {
		if !slices.Contains(processes, key) && !slices.Contains(chartWide, key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)

	return keys
}

// missing reports what block lacks for the root setting at path, whose value is rootValue
// and whose key in block is key. The key only has to be present: `replicas: ~` counts. A
// block that spells a map out (`image:` with its repository, pullPolicy and tag) must spell
// out every key the root map has; an empty map does not.
func missing(process, path string, rootValue any, block map[string]any, key string) []string {
	blockValue, present := block[key]
	if !present {
		return []string{fmt.Sprintf("%s: %s is missing, every root pod setting is repeated in each process block",
			process, path)}
	}

	rootMap, rootIsMap := rootValue.(map[string]any)
	blockMap, blockIsMap := blockValue.(map[string]any)
	if !rootIsMap || !blockIsMap || len(blockMap) == 0 {
		return nil
	}

	var problems []string
	for _, k := range slices.Sorted(maps.Keys(rootMap)) {
		problems = append(problems, missing(process, path+"."+k, rootMap[k], blockMap, k)...)
	}

	return problems
}
