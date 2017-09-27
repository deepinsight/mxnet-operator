// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package k8sutil

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/deepinsight/mxnet-operator/pkg/spec"
	"github.com/deepinsight/mxnet-operator/pkg/util"
	log "github.com/golang/glog"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/rest"
)

// MXJobClient defines an interface for working with MxJob CRDs.
type MxJobClient interface {
	// Get returns a MxJob
	Get(ns string, name string) (*spec.MxJob, error)

	// Delete a MxJob
	Delete(ns string, name string) (*spec.MxJob, error)

	// List returns a list of MxJobs
	List(ns string) (*spec.MxJobList, error)

	// Create a MxJob
	Create(ns string, j *spec.MxJob) (*spec.MxJob, error)

	// Update a MxJob.
	Update(ns string, j *spec.MxJob) (*spec.MxJob, error)

	// Watch MxJobs.
	Watch(host, ns string, httpClient *http.Client, resourceVersion string) (*http.Response, error)
}

// MxJobRestClient uses the Kubernetes rest interface to talk to the CRD.
type MxJobRestClient struct {
	restcli *rest.RESTClient
}

func NewMxJobClient() (*MxJobRestClient, error) {
	config, err := GetClusterConfig()
	if err != nil {
		return nil, err
	}
	config.GroupVersion = &spec.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: api.Codecs}

	restcli, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}

	cli := &MxJobRestClient{
		restcli: restcli,
	}
	return cli, nil
}

// New MXJob client for out-of-cluster
func NewMxJobClientExternal(config *rest.Config) (*MxJobRestClient, error) {

	config.GroupVersion = &schema.GroupVersion{
		Group:   spec.CRDGroup,
		Version: spec.CRDVersion,
	}
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: api.Codecs}

	restcli, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}

	cli := &MxJobRestClient{
		restcli: restcli,
	}
	return cli, nil
}

// HttpClient returns the http client used.
func (c *MxJobRestClient) Client() *http.Client {
	return c.restcli.Client
}

func (c *MxJobRestClient) Watch(host, ns string, httpClient *http.Client, resourceVersion string) (*http.Response, error) {
	return c.restcli.Client.Get(fmt.Sprintf("%s/apis/%s/%s/%s?watch=true&resourceVersion=%s",
		host, spec.CRDGroup, spec.CRDVersion, spec.CRDKindPlural, resourceVersion))
}

func (c *MxJobRestClient) List(ns string) (*spec.MxJobList, error) {
	b, err := c.restcli.Get().RequestURI(listMxJobsURI(ns)).DoRaw()
	if err != nil {
		return nil, err
	}

	jobs := &spec.MxJobList{}
	if err := json.Unmarshal(b, jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func listMxJobsURI(ns string) string {
	return fmt.Sprintf("/apis/%s/%s/%s", spec.CRDGroup, spec.CRDVersion, spec.CRDKindPlural)
}

func (c *MxJobRestClient) Create(ns string, j *spec.MxJob) (*spec.MxJob, error) {
	// Set the TypeMeta or we will get a BadRequest
	j.TypeMeta.APIVersion = fmt.Sprintf("%v/%v", spec.CRDGroup, spec.CRDVersion)
	j.TypeMeta.Kind = spec.CRDKind
	b, err := c.restcli.Post().Resource(spec.CRDKindPlural).Namespace(ns).Body(j).DoRaw()
	if err != nil {
		log.Errorf("Creating the MxJob:\n%v\nError:\n%v", util.Pformat(j), util.Pformat(err))
		return nil, err
	}
	return readOutMxJob(b)
}

func (c *MxJobRestClient) Get(ns, name string) (*spec.MxJob, error) {
	b, err := c.restcli.Get().Resource(spec.CRDKindPlural).Namespace(ns).Name(name).DoRaw()
	if err != nil {
		return nil, err
	}
	return readOutMxJob(b)
}

func (c *MxJobRestClient) Delete(ns, name string) (*spec.MxJob, error) {
	b, err := c.restcli.Delete().Resource(spec.CRDKindPlural).Namespace(ns).Name(name).DoRaw()
	if err != nil {
		return nil, err
	}
	return readOutMxJob(b)
}

func (c *MxJobRestClient) Update(ns string, j *spec.MxJob) (*spec.MxJob, error) {
	// Set the TypeMeta or we will get a BadRequest
	j.TypeMeta.APIVersion = fmt.Sprintf("%v/%v", spec.CRDGroup, spec.CRDVersion)
	j.TypeMeta.Kind = spec.CRDKind
	b, err := c.restcli.Put().Resource(spec.CRDKindPlural).Namespace(ns).Name(j.Metadata.Name).Body(j).DoRaw()
	if err != nil {
		return nil, err
	}
	return readOutMxJob(b)
}

func readOutMxJob(b []byte) (*spec.MxJob, error) {
	cluster := &spec.MxJob{}
	if err := json.Unmarshal(b, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}
