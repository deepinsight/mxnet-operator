package spec

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MxJobList is a list of etcd clusters.
type MxJobList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	Metadata metav1.ListMeta `json:"metadata,omitempty"`
	// Items is a list of third party objects
	Items []MxJob `json:"items"`
}

// There is known issue with TPR in client-go:
//   https://github.com/kubernetes/client-go/issues/8
// Workarounds:
// - We include `Metadata` field in object explicitly.
// - we have the code below to work around a known problem with third-party resources and ugorji.

// MxJobListCopy for MxJobList
type MxJobListCopy MxJobList

// MxJobCopy for MxJob
type MxJobCopy MxJob

// UnmarshalJSON for MxJob
func (c *MxJob) UnmarshalJSON(data []byte) error {
	tmp := MxJobCopy{}
	err := json.Unmarshal(data, &tmp)
	if err != nil {
		return err
	}
	tmp2 := MxJob(tmp)
	*c = tmp2
	return nil
}

// UnmarshalJSON for MxJobList
func (cl *MxJobList) UnmarshalJSON(data []byte) error {
	tmp := MxJobListCopy{}
	err := json.Unmarshal(data, &tmp)
	if err != nil {
		return err
	}
	tmp2 := MxJobList(tmp)
	*cl = tmp2
	return nil
}
