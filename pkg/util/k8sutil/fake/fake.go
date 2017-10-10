// fake provides a fake implementation of MxJobClient suitable for use in testing.
package fake

import (
	"net/http"
	"time"

	"github.com/deepinsight/mxnet-operator/pkg/spec"
)

type MxJobClientFake struct{}

func (c *MxJobClientFake) Get(ns string, name string) (*spec.MxJob, error) {
	return &spec.MxJob{}, nil
}

func (c *MxJobClientFake) Delete(ns string, name string) (*spec.MxJob, error) {
	return &spec.MxJob{}, nil
}

func (c *MxJobClientFake) List(ns string) (*spec.MxJobList, error) {
	return &spec.MxJobList{}, nil
}

func (c *MxJobClientFake) Create(ns string, j *spec.MxJob) (*spec.MxJob, error) {
	result := *j
	return &result, nil
}

// Update a MxJob.
func (c *MxJobClientFake) Update(ns string, j *spec.MxJob) (*spec.MxJob, error) {
	// TODO(jlewi): We should return a deep copy of j.
	result := *j
	return &result, nil
}

// Watch MxJobs.
func (c *MxJobClientFake) Watch(host, ns string, httpClient *http.Client, resourceVersion string) (*http.Response, error) {
	return nil, nil
}

// WaitTPRReady blocks until the MxJob TPR is ready.
func (c *MxJobClientFake) WaitTPRReady(interval, timeout time.Duration, ns string) error {
	return nil
}
