package filter

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodFilter_Match(t *testing.T) {
	pod := func(labels, annotations map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      labels,
				Annotations: annotations,
			},
		}
	}

	tests := []struct {
		name               string
		includeLabels      []string
		excludeLabels      []string
		includeAnnotations []string
		excludeAnnotations []string
		pod                *corev1.Pod
		shouldMatch        bool
	}{
		{
			name:        "all filters empty matches any pod",
			pod:         pod(map[string]string{"app": "foo"}, nil),
			shouldMatch: true,
		},
		{
			name:          "include label equality matches",
			includeLabels: []string{"app=foo"},
			pod:           pod(map[string]string{"app": "foo"}, nil),
			shouldMatch:   true,
		},
		{
			name:          "include label equality rejects non-matching pod",
			includeLabels: []string{"app=foo"},
			pod:           pod(map[string]string{"app": "bar"}, nil),
			shouldMatch:   false,
		},
		{
			name:          "exclude label equality removes matching pod",
			excludeLabels: []string{"cnpg.io/podRole=instance"},
			pod:           pod(map[string]string{"cnpg.io/podRole": "instance"}, nil),
			shouldMatch:   false,
		},
		{
			name:          "exclude label equality keeps non-matching pod",
			excludeLabels: []string{"cnpg.io/podRole=instance"},
			pod:           pod(map[string]string{"cnpg.io/podRole": "primary"}, nil),
			shouldMatch:   true,
		},
		{
			name:          "exclude wins on tie",
			includeLabels: []string{"app=foo"},
			excludeLabels: []string{"app=foo"},
			pod:           pod(map[string]string{"app": "foo"}, nil),
			shouldMatch:   false,
		},
		{
			name:               "presence-only include annotation matches when key exists",
			includeAnnotations: []string{"my.company.com/custom"},
			pod:                pod(nil, map[string]string{"my.company.com/custom": "anything"}),
			shouldMatch:        true,
		},
		{
			name:               "presence-only include annotation rejects pod without key",
			includeAnnotations: []string{"my.company.com/custom"},
			pod:                pod(nil, map[string]string{"other": "value"}),
			shouldMatch:        false,
		},
		{
			name:          "absence operator excludes pods with the key",
			includeLabels: []string{"!cnpg.io/podRole"},
			pod:           pod(map[string]string{"cnpg.io/podRole": "instance"}, nil),
			shouldMatch:   false,
		},
		{
			name:          "absence operator keeps pods without the key",
			includeLabels: []string{"!cnpg.io/podRole"},
			pod:           pod(map[string]string{"app": "foo"}, nil),
			shouldMatch:   true,
		},
		{
			name:          "set-based include narrows to listed values",
			includeLabels: []string{"tier in (frontend,backend)"},
			pod:           pod(map[string]string{"tier": "backend"}, nil),
			shouldMatch:   true,
		},
		{
			name:          "set-based include rejects values outside the set",
			includeLabels: []string{"tier in (frontend,backend)"},
			pod:           pod(map[string]string{"tier": "db"}, nil),
			shouldMatch:   false,
		},
		{
			name:          "multiple include entries are OR'd",
			includeLabels: []string{"app=foo", "app=bar"},
			pod:           pod(map[string]string{"app": "bar"}, nil),
			shouldMatch:   true,
		},
		{
			name:          "multiple exclude entries are OR'd",
			excludeLabels: []string{"app=foo", "app=bar"},
			pod:           pod(map[string]string{"app": "bar"}, nil),
			shouldMatch:   false,
		},
		{
			name:               "label and annotation filters are independent",
			includeLabels:      []string{"app=foo"},
			includeAnnotations: []string{"trace=on"},
			pod:                pod(map[string]string{"app": "foo"}, map[string]string{"trace": "off"}),
			shouldMatch:        false,
		},
		{
			name:               "both label and annotation filters must pass",
			includeLabels:      []string{"app=foo"},
			includeAnnotations: []string{"trace=on"},
			pod:                pod(map[string]string{"app": "foo"}, map[string]string{"trace": "on"}),
			shouldMatch:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := CompilePodFilter(tt.includeLabels, tt.excludeLabels, tt.includeAnnotations, tt.excludeAnnotations)
			if err != nil {
				t.Fatalf("CompilePodFilter() error = %v", err)
			}
			if got := f.Match(tt.pod); got != tt.shouldMatch {
				t.Errorf("Match() = %v, want %v", got, tt.shouldMatch)
			}
		})
	}
}

func TestPodFilter_CompileErrors(t *testing.T) {
	tests := []struct {
		name          string
		includeLabels []string
	}{
		{name: "garbage selector", includeLabels: []string{"==="}},
		{name: "invalid key", includeLabels: []string{"BAD KEY=value"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CompilePodFilter(tt.includeLabels, nil, nil, nil); err == nil {
				t.Errorf("expected CompilePodFilter to fail on invalid selector %q", tt.includeLabels)
			}
		})
	}
}
