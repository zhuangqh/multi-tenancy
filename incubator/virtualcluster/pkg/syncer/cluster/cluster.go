/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	restclient "k8s.io/client-go/rest"
	clientgocache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/kubernetes-sigs/multi-tenancy/incubator/virtualcluster/pkg/apis/tenancy/v1alpha1"
	vclisters "github.com/kubernetes-sigs/multi-tenancy/incubator/virtualcluster/pkg/client/listers/tenancy/v1alpha1"
	"github.com/kubernetes-sigs/multi-tenancy/incubator/virtualcluster/pkg/syncer/constants"
	"github.com/kubernetes-sigs/multi-tenancy/incubator/virtualcluster/pkg/syncer/mccontroller"
	"github.com/kubernetes-sigs/multi-tenancy/incubator/virtualcluster/pkg/syncer/reconciler"
)

// Each Cluster object represents a tenant master in Virtual Cluster architecture.
//
// Cluster implements the ClusterInterface used by MultiClusterController in
// github.com/kubernetes-sigs/multi-tenancy/incubator/virtualcluster/pkg/syncer/mccontroller/mccontroller.go.
//
// It stores a Kubernetes client, cache, and other cluster-scoped dependencies.
// The dependencies are lazily created in getters and cached for reuse.
// It is not thread safe.
type Cluster struct {
	// Name of the corresponding virtual cluster object.
	VCName string
	// Namespace of the corresponding virtual cluster object.
	VCNamespace string

	// KubeClientConfig is used to make it easy to get an api server client. Required.
	KubeClientConfig clientcmd.ClientConfig

	// Config is the rest.config used to talk to the apiserver.  Required.
	RestConfig *rest.Config

	// vcLister points to the super master virtual cluster informer cache.
	vclister vclisters.VirtualclusterLister

	// APIServerHost is the host field of apiserver from tenant admin kubeconfig.
	// or a URL to the base of the apiserver.
	APIServerHost string

	// spec is the vc definition. Required.
	spec *v1alpha1.VirtualclusterSpec

	// scheme is injected by the controllerManager when controllerManager.Start is called
	scheme *runtime.Scheme

	mapper meta.RESTMapper

	// informer cache and delegating client for watched tenant master objects
	cache            cache.Cache
	delegatingClient *client.DelegatingClient

	// a clientset client for unwatched tenant master objects (rw directly to tenant apiserver)
	client *clientset.Clientset

	Options

	// a flag indicates that the cluster cache has been synced
	synced bool

	stopCh chan struct{}
}

// Options are the arguments for creating a new Cluster.
type Options struct {
	CacheOptions
}

// CacheOptions is embedded in Options to configure the new Cluster's cache.
type CacheOptions struct {
	// Resync is the period between cache resyncs.
	// A cache resync triggers event handlers for each object watched by the cache.
	// It can be useful if your level-based logic isn't perfect.
	Resync *time.Duration
	// Namespace can be used to watch only a single namespace.
	// If unset (Namespace == ""), all namespaces are watched.
	Namespace string
}

var _ mccontroller.ClusterInterface = &Cluster{}

// New creates a new Cluster.
func NewTenantCluster(namespace, name string, vclister vclisters.VirtualclusterLister, configBytes []byte, o Options) (*Cluster, error) {
	clusterRestConfig, err := clientcmd.RESTConfigFromKubeConfig(configBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to build rest config: %v", err)
	}

	kubeClientConfig, err := clientcmd.NewClientConfigFromBytes(configBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to build kube client config: %v", err)
	}

	apiServerAddress, err := findAPIServerHost(kubeClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to find apiserver address: %v", err)
	}

	return &Cluster{
		VCName:           name,
		VCNamespace:      namespace,
		vclister:         vclister,
		KubeClientConfig: kubeClientConfig,
		RestConfig:       clusterRestConfig,
		APIServerHost:    apiServerAddress,
		Options:          o,
		synced:           false,
		stopCh:           make(chan struct{})}, nil
}

// GetClusterName returns the unique cluster name, aka, the full name of virtual cluster CRD.
func findAPIServerHost(clientConfig clientcmd.ClientConfig) (string, error) {
	config, err := clientConfig.ClientConfig()
	if err != nil {
		return "", err
	}

	hostURL, err := url.Parse(config.Host)
	if err != nil {
		return "", err
	}

	host := hostURL.Host
	if idx := strings.IndexByte(host, ':'); idx != -1 {
		host = host[:idx]
	}

	return host, nil
}

// GetClusterName returns the name given when Cluster c was created.
>>>>>>> inject apiserver host to pod annotations
func (c *Cluster) GetClusterName() string {
	return c.VCNamespace + "-" + c.VCName
}

// GetAPIServerHost returns the apiserver host of the tenant.
func (c *Cluster) GetAPIServerHost() string {
	return c.APIServerHost
}

// GetSpec returns the virtual cluster spec.
func (c *Cluster) GetSpec() (*v1alpha1.VirtualclusterSpec, error) {
	vc, err := c.vclister.Virtualclusters(c.VCNamespace).Get(c.VCName)
	if err != nil {
		return nil, err
	}

	spec := vc.Spec.DeepCopy()
	prefixesSet := sets.NewString(spec.OpaqueMetaPrefixes...)
	if !prefixesSet.Has(constants.DefaultOpaqueMetaPrefix) {
		spec.OpaqueMetaPrefixes = append(spec.OpaqueMetaPrefixes, constants.DefaultOpaqueMetaPrefix)
	}

	return vc.Spec.DeepCopy(), nil
}

func (c *Cluster) getScheme() *runtime.Scheme {
	return scheme.Scheme
}

// GetClientInfo returns the cluster client info.
func (c *Cluster) GetClientInfo() *reconciler.ClusterInfo {
	return reconciler.NewClusterInfo(c.GetClusterName(), c.RestConfig)
}

// GetClientSet returns a clientset client without any informer caches. All client requests go to apiserver directly.
func (c *Cluster) GetClientSet() (*clientset.Clientset, error) {
	if c.client != nil {
		return c.client, nil
	}
	var err error
	c.client, err = clientset.NewForConfig(restclient.AddUserAgent(c.RestConfig, constants.ResourceSyncerUserAgent))
	if err != nil {
		return nil, err
	}
	return c.client, nil
}

// GetClientConfig return clientConfig used to make it easy to get an api server client.
func (c *Cluster) GetClientConfig() clientcmd.ClientConfig {
	return c.KubeClientConfig
}

// getMapper returns a lazily created apimachinery RESTMapper.
func (c *Cluster) getMapper() (meta.RESTMapper, error) {
	if c.mapper != nil {
		return c.mapper, nil
	}

	mapper, err := apiutil.NewDiscoveryRESTMapper(c.RestConfig)
	if err != nil {
		return nil, err
	}

	c.mapper = mapper
	return mapper, nil
}

// getCache returns a lazily created controller-runtime Cache.
func (c *Cluster) getCache() (cache.Cache, error) {
	if c.cache != nil {
		return c.cache, nil
	}

	m, err := c.getMapper()
	if err != nil {
		return nil, err
	}

	ca, err := cache.New(c.RestConfig, cache.Options{
		Scheme:    c.getScheme(),
		Mapper:    m,
		Resync:    c.Resync,
		Namespace: c.Namespace,
	})
	if err != nil {
		return nil, err
	}

	c.cache = ca
	return ca, nil
}

// GetDelegatingClient returns a lazily created controller-runtime DelegatingClient.
// It is used by other Cluster getters, and by reconcilers.
// TODO: consider implementing Reader, Writer and StatusClient in Cluster
// and forwarding to actual delegating client.
func (c *Cluster) GetDelegatingClient() (*client.DelegatingClient, error) {
	if !c.synced {
		return nil, fmt.Errorf("The client cache has not been synced yet.")
	}

	if c.delegatingClient != nil {
		return c.delegatingClient, nil
	}

	ca, err := c.getCache()
	if err != nil {
		return nil, err
	}

	m, err := c.getMapper()
	if err != nil {
		return nil, err
	}

	cl, err := client.New(c.RestConfig, client.Options{
		Scheme: c.getScheme(),
		Mapper: m,
	})
	if err != nil {
		return nil, err
	}

	dc := &client.DelegatingClient{
		Reader: &client.DelegatingReader{
			CacheReader:  ca,
			ClientReader: cl,
		},
		Writer:       cl,
		StatusClient: cl,
	}

	c.delegatingClient = dc
	return dc, nil
}

// AddEventHandler instructs the Cluster's cache to watch objectType's resource,
// if it doesn't already, and to add handler as an event handler.
func (c *Cluster) AddEventHandler(objectType runtime.Object, handler clientgocache.ResourceEventHandler) error {
	ca, err := c.getCache()
	if err != nil {
		return err
	}

	i, err := ca.GetInformer(objectType)
	if err != nil {
		return err
	}

	i.AddEventHandler(handler)
	return nil
}

// Start starts the Cluster's cache and blocks,
// until an empty struct is sent to the stop channel.
func (c *Cluster) Start() error {
	ca, err := c.getCache()
	if err != nil {
		return err
	}
	return ca.Start(c.stopCh)
}

// WaitForCacheSync waits for the Cluster's cache to sync,
// OR until an empty struct is sent to the stop channel.
func (c *Cluster) WaitForCacheSync() bool {
	ca, err := c.getCache()
	if err != nil {
		return false
	}
	return ca.WaitForCacheSync(c.stopCh)
}

func (c *Cluster) SetSynced() {
	c.synced = true
}

// Stop send a msg to stopCh, stop the cache.
func (c *Cluster) Stop() {
	close(c.stopCh)
}
