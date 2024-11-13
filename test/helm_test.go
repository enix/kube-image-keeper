package test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func unmarshal[T any](t *testing.T, data string, obj *T) {
	require.NoError(t, helm.UnmarshalK8SYamlE(t, data, obj))
}

func makeAssertion[T any](assertion func(t *testing.T, obj *T)) func(t *testing.T, output string) {
	return func(t *testing.T, output string) {
		obj := new(T)
		typeMeta := &metav1.TypeMeta{}
		unmarshal(t, output, obj)
		unmarshal(t, output, typeMeta)

		require.Equal(t, reflect.TypeOf(obj).Elem().Name(), typeMeta.Kind)

		assertion(t, obj)
	}
}

func makeSliceAssertion[T any](assertion func(t *testing.T, objs []T)) func(t *testing.T, output string) {
	return func(t *testing.T, output string) {
		documents := strings.Split(output, "\n---\n")
		objs := make([]T, len(documents))

		for i, document := range documents {
			unmarshal(t, document, &objs[i])
			assertion(t, objs)
		}
	}
}

func TestHelmTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		values     map[string]string
		assertions map[string](func(t *testing.T, output string))
	}{
		{
			name:   "Default install",
			values: map[string]string{},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-pvc.yaml":        nil,
				"registry-deployment.yaml": nil,
				"registry-statefulset.yaml": makeAssertion(func(t *testing.T, obj *appsv1.StatefulSet) {
					assert := assert.New(t)
					assert.Len(obj.Spec.Template.Spec.Volumes, 0, "should have 0 volume")
					assert.Len(obj.Spec.VolumeClaimTemplates, 0, "should have 0 volume claim template")
				}),
			},
		},
		{
			name: "Enabled registry local ReadWriteMany persistence",
			values: map[string]string{
				"registry.persistence.enabled":     "true",
				"registry.persistence.accessModes": "ReadWriteMany",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-deployment.yaml": nil,
				"registry-pvc.yaml": makeAssertion(func(t *testing.T, obj *v1.PersistentVolumeClaim) {
					assert := assert.New(t)
					assert.Equal("kube-image-keeper-registry-pvc", obj.Name)
				}),
				"registry-statefulset.yaml": makeAssertion(func(t *testing.T, obj *appsv1.StatefulSet) {
					assert := assert.New(t)
					if assert.Len(obj.Spec.Template.Spec.Volumes, 1, "should have 1 volume") {
						assert.Equal(obj.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName, "kube-image-keeper-registry-pvc")
					}
				}),
			},
		},
		{
			name: "Enabled registry local ReadWriteOnce persistence",
			values: map[string]string{
				"registry.persistence.enabled": "true",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-deployment.yaml": nil,
				"registry-pvc.yaml":        nil,
				"registry-statefulset.yaml": makeAssertion(func(t *testing.T, obj *appsv1.StatefulSet) {
					assert := assert.New(t)
					assert.Len(obj.Spec.Template.Spec.Volumes, 0, "should have 0 volume")
				}),
			},
		},
		{
			name: "Enabled registry persistence with minio",
			values: map[string]string{
				"minio.enabled": "true",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-statefulset.yaml": nil,
				"registry-pvc.yaml":         nil,
				"registry-deployment.yaml": makeAssertion(func(t *testing.T, obj *appsv1.Deployment) {
					assert := assert.New(t)
					assert.Len(obj.Spec.Template.Spec.Volumes, 0, "should have 0 volume")
					require.Len(t, obj.Spec.Template.Spec.Containers, 1)
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE",
						Value: "s3",
					})
					localObjectReference := v1.LocalObjectReference{
						Name: "kube-image-keeper-s3-registry-keys",
					}
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name: "REGISTRY_STORAGE_S3_ACCESSKEY",
						ValueFrom: &v1.EnvVarSource{
							SecretKeyRef: &v1.SecretKeySelector{
								LocalObjectReference: localObjectReference,
								Key:                  "accessKey",
							},
						},
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name: "REGISTRY_STORAGE_S3_SECRETKEY",
						ValueFrom: &v1.EnvVarSource{
							SecretKeyRef: &v1.SecretKeySelector{
								LocalObjectReference: localObjectReference,
								Key:                  "secretKey",
							},
						},
					})
				}),
				"minio-registry-users.yaml": makeSliceAssertion(func(t *testing.T, objs []v1.Secret) {
					assert.Len(t, objs, 2)
				}),
				"s3-registry-keys.yaml": makeAssertion(func(t *testing.T, obj *v1.Secret) {
					assert := assert.New(t)
					assert.Len(obj.StringData["accessKey"], 16)
					assert.Len(obj.StringData["secretKey"], 32)
				}),
			},
		},
		{
			name: "Enabled registry persistence with S3",
			values: map[string]string{
				"registry.persistence.s3.accesskey": "ACCESS",
				"registry.persistence.s3.secretkey": "SECRET",
				"registry.persistence.s3.foo":       "BAR",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"s3-registry-keys.yaml": makeAssertion(func(t *testing.T, obj *v1.Secret) {
					assert := assert.New(t)
					assert.Equal("ACCESS", obj.StringData["accessKey"])
					assert.Equal("SECRET", obj.StringData["secretKey"])
				}),
				"registry-deployment.yaml": makeAssertion(func(t *testing.T, obj *appsv1.Deployment) {
					assert := assert.New(t)
					require.Len(t, obj.Spec.Template.Spec.Containers, 1)
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE",
						Value: "s3",
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE_S3_FOO",
						Value: "BAR",
					})
					for _, env := range obj.Spec.Template.Spec.Containers[0].Env {
						assert.NotEqual("REGISTRY_STORAGE_S3_ACCESSKEY", env.Name)
						assert.NotEqual("REGISTRY_STORAGE_S3_SECRETKEY", env.Name)
					}
				}),
			},
		},
		{
			name: "Enabled registry persistence with S3 and existing secret",
			values: map[string]string{
				"registry.persistence.s3ExistingSecret": "NAME",
				"registry.persistence.s3.foo":           "BAR",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"s3-registry-keys.yaml": nil,
				"registry-deployment.yaml": makeAssertion(func(t *testing.T, obj *appsv1.Deployment) {
					assert := assert.New(t)
					require.Len(t, obj.Spec.Template.Spec.Containers, 1)
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE",
						Value: "s3",
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE_S3_FOO",
						Value: "BAR",
					})
					localObjectReference := v1.LocalObjectReference{
						Name: "NAME",
					}
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name: "REGISTRY_STORAGE_S3_ACCESSKEY",
						ValueFrom: &v1.EnvVarSource{
							SecretKeyRef: &v1.SecretKeySelector{
								LocalObjectReference: localObjectReference,
								Key:                  "accessKey",
							},
						},
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name: "REGISTRY_STORAGE_S3_SECRETKEY",
						ValueFrom: &v1.EnvVarSource{
							SecretKeyRef: &v1.SecretKeySelector{
								LocalObjectReference: localObjectReference,
								Key:                  "secretKey",
							},
						},
					})
				}),
			},
		},
		{
			name: "Enabled registry persistence with GCS",
			values: map[string]string{
				"registry.persistence.gcs.bucket": "BUCKET",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-deployment.yaml": makeAssertion(func(t *testing.T, obj *appsv1.Deployment) {
					assert := assert.New(t)
					require.Len(t, obj.Spec.Template.Spec.Containers, 1)
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE",
						Value: "gcs",
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE_GCS_BUCKET",
						Value: "BUCKET",
					})
				}),
			},
		},
		{
			name: "Enabled registry persistence with GCS and existing secret",
			values: map[string]string{
				"registry.persistence.gcs.bucket":        "BUCKET",
				"registry.persistence.gcsExistingSecret": "SECRET",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-deployment.yaml": makeAssertion(func(t *testing.T, obj *appsv1.Deployment) {
					assert := assert.New(t)
					require.Len(t, obj.Spec.Template.Spec.Containers, 1)
					assert.Len(obj.Spec.Template.Spec.Volumes, 1)
					assert.Len(obj.Spec.Template.Spec.Containers[0].VolumeMounts, 1)
					assert.Contains(obj.Spec.Template.Spec.Volumes, v1.Volume{
						Name: "gcs-key",
						VolumeSource: v1.VolumeSource{
							Secret: &v1.SecretVolumeSource{
								SecretName: "SECRET",
							},
						},
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].VolumeMounts, v1.VolumeMount{
						Name:      "gcs-key",
						MountPath: "/etc/registry/keys",
						ReadOnly:  true,
					})
				}),
			},
		},
		{
			name: "Enabled registry persistence with Azure",
			values: map[string]string{
				"registry.persistence.azure.accountname": "NAME",
				"registry.persistence.azure.accountkey":  "KEY",
				"registry.persistence.azure.container":   "CONTAINER",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-deployment.yaml": makeAssertion(func(t *testing.T, obj *appsv1.Deployment) {
					assert := assert.New(t)
					require.Len(t, obj.Spec.Template.Spec.Containers, 1)
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE",
						Value: "azure",
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name:  "REGISTRY_STORAGE_AZURE_CONTAINER",
						Value: "CONTAINER",
					})
					for _, env := range obj.Spec.Template.Spec.Containers[0].Env {
						assert.NotEqual("REGISTRY_STORAGE_AZURE_ACCOUNTNAME", env.Name)
						assert.NotEqual("REGISTRY_STORAGE_AZURE_ACCOUNTKEY", env.Name)
					}
				}),
			},
		},
		{
			name: "Enabled registry persistence with Azure and existing secret",
			values: map[string]string{
				"registry.persistence.azure.container":     "CONTAINER",
				"registry.persistence.azureExistingSecret": "SECRET",
			},
			assertions: map[string](func(t *testing.T, output string)){
				"registry-deployment.yaml": makeAssertion(func(t *testing.T, obj *appsv1.Deployment) {
					assert := assert.New(t)
					require.Len(t, obj.Spec.Template.Spec.Containers, 1)
					localObjectReference := v1.LocalObjectReference{
						Name: "SECRET",
					}
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name: "REGISTRY_STORAGE_AZURE_ACCOUNTNAME",
						ValueFrom: &v1.EnvVarSource{
							SecretKeyRef: &v1.SecretKeySelector{
								LocalObjectReference: localObjectReference,
								Key:                  "accountname",
							},
						},
					})
					assert.Contains(obj.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
						Name: "REGISTRY_STORAGE_AZURE_ACCOUNTKEY",
						ValueFrom: &v1.EnvVarSource{
							SecretKeyRef: &v1.SecretKeySelector{
								LocalObjectReference: localObjectReference,
								Key:                  "accountkey",
							},
						},
					})
				}),
			},
		},
	}

	releaseName := "kube-image-keeper"
	helmChartPath, err := filepath.Abs("../helm/kube-image-keeper")
	require.NoError(t, err)

	helm.AddRepo(t, &helm.Options{}, "bitnami", "https://charts.bitnami.com/bitnami")
	helm.AddRepo(t, &helm.Options{}, "joxit", "https://helm.joxit.dev")

	output, err := helm.RunHelmCommandAndGetOutputE(t, &helm.Options{}, "dependency", "list", helmChartPath)
	require.NoError(t, err)

	if strings.Contains(output, "missing") {
		_, err := helm.RunHelmCommandAndGetOutputE(t, &helm.Options{}, "dependency", "build", helmChartPath)
		require.NoError(t, err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := &helm.Options{
				SetValues: tt.values,
				Logger:    logger.Discard,
			}

			for templateName, assertion := range tt.assertions {
				t.Run(templateName, func(t *testing.T) {
					require := require.New(t)
					output, err := helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/" + templateName})

					if assertion != nil {
						require.NoError(err)
						assertion(t, output)
					} else {
						require.Error(err)
					}
				})
			}
		})
	}
}
