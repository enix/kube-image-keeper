package cache

import (
	"context"
	"regexp"
	"strings"

	dcrenixiov1alpha1 "gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

var scheme *runtime.Scheme

func init() {
	InitScheme()
}

type Cache struct {
	client.Client
}

func New() (*Cache, error) {
	client, err := newReadOnlyClient()
	if err != nil {
		return nil, err
	}

	return &Cache{
		Client: client,
	}, nil
}

func newReadOnlyClient() (client.Client, error) {
	config, err := ctrl.GetConfig()
	if err != nil {
		return nil, err
	}

	mapper, err := apiutil.NewDynamicRESTMapper(config)
	if err != nil {
		return nil, err
	}

	readOnlyClient, err := client.New(config, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, err
	}

	return readOnlyClient, nil
}

func InitScheme() *runtime.Scheme {
	scheme = runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(dcrenixiov1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme

	return scheme
}

func SanitizeImageName(image string) string {
	nameRegex := regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)
	return strings.Join(nameRegex.FindAllString(image, -1), "-")
}

func (c *Cache) GetCachedImage(image string) (*dcrenixiov1alpha1.CachedImage, error) {
	sanitizedName := SanitizeImageName(image)
	var cachedImage dcrenixiov1alpha1.CachedImage

	err := c.Get(context.Background(), types.NamespacedName{Name: sanitizedName}, &cachedImage)
	if err != nil {
		return nil, err
	}

	return &cachedImage, nil
}
