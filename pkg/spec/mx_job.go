package spec

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/deepinsight/mxnet-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
	//"github.com/golang/protobuf/proto"
	"github.com/gogo/protobuf/proto"
)

const (
	// CRDKind k8s crd kind
	CRDKind = "MxJob"
	// CRDKindPlural k8s crd Plural
	CRDKindPlural = "mxjobs"
	// CRDGroup k8s crd group
	CRDGroup = "mxnet.mlkube.io"
	// CRDVersion k8s crd version
	CRDVersion = "v1beta1"
	// CRDApiVersion k8s crd api version
	CRDApiVersion = CRDGroup + "/" + CRDVersion // "mlkube.io/v1beta1"

	// AppLabel Value of the APP label that gets applied to a lot of entities.
	AppLabel = "mxnet-job"

	// PsRootPort Defaults for the Spec
	PsRootPort = 9091
	// Replicas Defaults for the Spec
	Replicas = 1
)

// CRDName return crd name
func CRDName() string {
	return fmt.Sprintf("%s.%s", CRDKindPlural, CRDGroup)
}

// MxJob mxnet job
type MxJob struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec            MxJobSpec         `json:"spec"`
	Status          MxJobStatus       `json:"status"`
}

// AsOwner return owner reference
func (j *MxJob) AsOwner() metav1.OwnerReference {
	trueVar := true
	// TODO: In 1.6 this is gonna be "k8s.io/kubernetes/pkg/apis/meta/v1"
	// Both api.OwnerReference and metatypes.OwnerReference are combined into that.
	return metav1.OwnerReference{
		APIVersion: j.APIVersion,
		Kind:       j.Kind,
		Name:       j.Metadata.Name,
		UID:        j.Metadata.UID,
		Controller: &trueVar,
	}
}

// Key is an unique key for MxJob to store in maps
func (j *MxJob) Key() string {
	return j.Metadata.Namespace + "-" + j.Metadata.Name
}

// JobMode mxnet job mode
type JobMode string

const (
	// LocalJob job kind local
	LocalJob JobMode = "local"
	// DistJob job kind distribution
	DistJob JobMode = "dist"
)

// TODO(jlewi): Need to define the actual configuration for the MXNet MxJob.

// MxJobSpec mxnet job specification
type MxJobSpec struct {
	// TODO(jlewi): Can we we get rid of this and use some value from Kubernetes or a random ide.

	// RuntimeId job id
	RuntimeId string

	// JobMode MXNet training job mode: local, dist
	JobMode `json:"jobMode"`

	// ReplicaSpecs specifies the Mx replicas to run.
	ReplicaSpecs []*MxReplicaSpec `json:"replicaSpecs"`
}

// MxReplicaType determines how a set of Mx processes are handled.
type MxReplicaType string

const (
	// SCHEDULER mxnet training job replica type
	SCHEDULER MxReplicaType = "SCHEDULER"
	// SERVER mxnet training job replica type
	SERVER MxReplicaType = "SERVER"
	// WORKER mxnet training job replica type
	WORKER MxReplicaType = "WORKER"
)

// ContainerName is an enum for expected containers.
type ContainerName string

const (
	// MXNET container name for mxnet training job
	MXNET ContainerName = "mxnet"
)

// TODO(jlewi): We probably want to add a name field. This would allow us to have more than 1 type of each worker.
// This might be useful if you wanted to have a separate set of workers to do eval.

// MxReplicaSpec mxnet replica specification
type MxReplicaSpec struct {
	// Replicas is the number of desired replicas.
	// This is a pointer to distinguish between explicit zero and unspecified.
	// Defaults to 1.
	// More info: http://kubernetes.io/docs/user-guide/replication-controller#what-is-a-replication-controller
	// +optional
	Replicas *int32              `json:"replicas,omitempty" protobuf:"varint,1,opt,name=replicas"`
	Template *v1.PodTemplateSpec `json:"template,omitempty" protobuf:"bytes,3,opt,name=template"`
	// Root_PS_Port is the port to use for scheduler.
	PsRootPort    *int32 `json:"PsRootPort,omitempty" protobuf:"varint,1,opt,name=PsRootPort"`
	MxReplicaType `json:"mxReplicaType"`
}

// Validate checks that the MxJobSpec is valid.
func (c *MxJobSpec) Validate() error {
	// Check that each replica has a MXNet container.
	replicaRoleMap := make(map[MxReplicaType]bool)
	replicaRoleMap[SCHEDULER] = false
	replicaRoleMap[SERVER] = false
	replicaRoleMap[WORKER] = false
	var workerNum int32
	for _, r := range c.ReplicaSpecs {
		found := false
		if r.Template == nil {
			return fmt.Errorf("Replica is missing Template; %v", util.Pformat(r))
		}

		// Make sure the replica type is valid.
		validReplicaTypes := []MxReplicaType{SCHEDULER, SERVER, WORKER}

		_, ok := replicaRoleMap[r.MxReplicaType]
		if !ok {
			return fmt.Errorf("MxReplicaSpec.MxReplicaType is %v but must be one of %v", r.MxReplicaType, validReplicaTypes)
		}

		replicaRoleMap[r.MxReplicaType] = true
		if r.MxReplicaType == WORKER {
			workerNum = *r.Replicas
		}

		for _, c := range r.Template.Spec.Containers {
			if c.Name == "mxnet" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Replica type %v is missing a container for mxnet", r.MxReplicaType)
		}
	}

	if c.JobMode == LocalJob {
		if replicaRoleMap[SCHEDULER] == true || replicaRoleMap[SERVER] == true {
			return fmt.Errorf("job mode is local, but its replicas set have replicas type other than worker")
		}
		if workerNum > 1 {
			return fmt.Errorf("job mode is local, but it has more than 1 worker")
		}
	} else if c.JobMode == DistJob {
		for r, ok := range replicaRoleMap {
			if ok == false {
				return fmt.Errorf("dist job mode without replica type %v", r)
			}
		}
	} else {
		return fmt.Errorf("unkonw job mode %v", c.JobMode)
	}

	return nil
}

// ConfigureAccelerators adds any accelerator specific configuration to the pods.
func (c *MxJobSpec) ConfigureAccelerators(accelerators map[string]AcceleratorConfig) error {
	for _, r := range c.ReplicaSpecs {
		if r.Template == nil {
			return fmt.Errorf("Replica is missing Template; %v", util.Pformat(r))
		}
		for i, c := range r.Template.Spec.Containers {
			if c.Name == string(MXNET) {
				// Identify the accelerators attached to this container.
				a := map[string]AcceleratorConfig{}

				lists := []v1.ResourceList{c.Resources.Limits, c.Resources.Requests}
				for _, resources := range lists {
					for name, _ := range resources {

						if _, ok := accelerators[string(name)]; !ok {
							continue
						}

						// Add the expected mounts to the pods.
						a[string(name)] = accelerators[string(name)]
					}
				}

				// Add accelerator information to the pod.
				for _, config := range a {
					for _, v := range config.Volumes {
						r.Template.Spec.Volumes = append(r.Template.Spec.Volumes,
							v1.Volume{
								Name: v.Name,
								VolumeSource: v1.VolumeSource{
									HostPath: &v1.HostPathVolumeSource{
										Path: v.HostPath,
									},
								},
							})
						c.VolumeMounts = append(c.VolumeMounts, v1.VolumeMount{
							Name:      v.Name,
							MountPath: v.MountPath,
						})
					}

					for _, envVar := range config.EnvVars {
						c.Env = append(c.Env, v1.EnvVar{
							Name:  envVar.Name,
							Value: envVar.Value,
						})
					}
				}
				r.Template.Spec.Containers[i] = c
				break
			}
		}
	}
	return nil
}

// SetDefaults sets any unspecified values to defaults
func (c *MxJobSpec) SetDefaults() error {
	// Check that each replica has a MXNet container.
	for _, r := range c.ReplicaSpecs {
		if r == nil {
			return fmt.Errorf("ReplicaSpecs contain nil")
		}

		if r.Template == nil {
			return fmt.Errorf("Replica is missing Template; %v", util.Pformat(r))
		}

		if r.PsRootPort == nil {
			r.PsRootPort = proto.Int32(PsRootPort)
		}

		if string(r.MxReplicaType) == "" {
			r.MxReplicaType = WORKER
		}

		if r.Replicas == nil {
			r.Replicas = proto.Int32(Replicas)
		}
	}
	return nil
}

// Cleanup cleans up user passed spec, e.g. defaulting, transforming fields.
// TODO: move this to admission controller
func (c *MxJobSpec) Cleanup() {
	// TODO(jlewi): Add logic to cleanup user provided spec; e.g. by filling in defaults.
	// We should have default container images so user doesn't have to provide these.
}

// MxJobPhase mxnet job phase
type MxJobPhase string

const (
	// MxJobPhaseNone job phase none
	MxJobPhaseNone MxJobPhase = ""
	// MxJobPhaseCreating job phase creating
	MxJobPhaseCreating = "Creating"
	// MxJobPhaseRunning job phase running
	MxJobPhaseRunning = "Running"
	// MxJobPhaseCleanUp job phase cleanup
	MxJobPhaseCleanUp = "CleanUp"
	// MxJobPhaseFailed job phase failed
	MxJobPhaseFailed = "Failed"
	// MxJobPhaseDone job phase done
	MxJobPhaseDone = "Done"
)

// MxJobCondition mxnet job condition
type MxJobCondition struct {
	Type MxJobConditionType `json:"type,omitempty"`

	Reason string `json:"reason,omitempty"`

	TransitionTime string `json:"transitionTime,omitempty"`
}

// MxJobConditionType mxnet job condition type
type MxJobConditionType string

// TODO(jlewi): Need to define appropriate conditions and get rid of the ones we don't need.
const (
	MxJobConditionReady = "Ready"

	MxJobConditionRemovingDeadMember = "RemovingDeadMember"

	MxJobConditionRecovering = "Recovering"

	MxJobConditionScalingUp   = "ScalingUp"
	MxJobConditionScalingDown = "ScalingDown"

	MxJobConditionUpgrading = "Upgrading"
)

// State mxnet job state
type State string

const (
	// StateUnknown state unknown
	StateUnknown State = "Unknown"
	// StateRunning state running
	StateRunning State = "Running"
	// StateSucceeded state succeeded
	StateSucceeded State = "Succeeded"
	// StateFailed state failed
	StateFailed State = "Failed"
)

// MxJobStatus mxnet job status
type MxJobStatus struct {
	// Phase is the MxJob running phase
	Phase  MxJobPhase `json:"phase,omitempty"`
	Reason string     `json:"reason,omitempty"`

	// ControlPuased indicates the operator pauses the control of the cluster.
	// TODO(jlewi): I think we can get rid of ControlPaued.
	ControlPaused bool `json:"controlPaused"`

	// Condition keeps ten most recent cluster conditions
	Conditions []MxJobCondition `json:"conditions,omitempty"`

	// State indicates the state of the job.
	State State `json:"state,omitempty"`

	// ReplicaStatuses specifies the status of each Mx replica.
	ReplicaStatuses []*MxReplicaStatus `json:"replicaStatuses"`
}

// ReplicaState mxnet job replica state
type ReplicaState string

const (
	// ReplicaStateUnknown replica state unknown
	ReplicaStateUnknown ReplicaState = "Unknown"
	// ReplicaStateStarting replica state starting
	ReplicaStateStarting = "Starting"
	// ReplicaStateRunning replica state running
	ReplicaStateRunning = "Running"
	// ReplicaStateFailed replica state failed
	ReplicaStateFailed = "Failed"
	// ReplicaStateSucceeded replica state succeeded
	ReplicaStateSucceeded = "Succeeded"
)

// MxReplicaStatus mxnet replica status
type MxReplicaStatus struct {
	MxReplicaType `json:"Mx_replica_type"`
	// State is the overall state of the replica
	State ReplicaState `json:"state"`

	// ReplicasStates provides the number of replicas in each status.
	ReplicasStates map[ReplicaState]int
}

// Copy mxnet job status
func (cs MxJobStatus) Copy() MxJobStatus {
	newCS := MxJobStatus{}
	b, err := json.Marshal(cs)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(b, &newCS)
	if err != nil {
		panic(err)
	}
	return newCS
}

// IsFailed return true if job status failed
func (cs *MxJobStatus) IsFailed() bool {
	if cs == nil {
		return false
	}
	return cs.State == StateFailed
}

// SetPhase set up mxnet job status phase
func (cs *MxJobStatus) SetPhase(p MxJobPhase) {
	cs.Phase = p
}

// PauseControl set cs ControlPaused = true
func (cs *MxJobStatus) PauseControl() {
	cs.ControlPaused = true
}

// Control set cs ControlPaused = false
func (cs *MxJobStatus) Control() {
	cs.ControlPaused = false
}

// SetReason for mxnet job status
func (cs *MxJobStatus) SetReason(r string) {
	cs.Reason = r
}

// SetState for mxnet job status
func (cs *MxJobStatus) SetState(s State) {
	cs.State = s
}

// TODO(jlewi): Get rid of the append methods that we don't need

// AppendScalingDownCondition for mxnet job status
func (cs *MxJobStatus) AppendScalingDownCondition(from, to int) {
	c := MxJobCondition{
		Type:           MxJobConditionScalingDown,
		Reason:         scalingReason(from, to),
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

// AppendRecoveringCondition for mxnet job status
func (cs *MxJobStatus) AppendRecoveringCondition() {
	c := MxJobCondition{
		Type:           MxJobConditionRecovering,
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

// AppendUpgradingCondition for mxnet job status
func (cs *MxJobStatus) AppendUpgradingCondition(to string, member string) {
	reason := fmt.Sprintf("upgrading cluster member %s version to %v", member, to)

	c := MxJobCondition{
		Type:           MxJobConditionUpgrading,
		Reason:         reason,
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

// AppendRemovingDeadMember for mxnet job status
func (cs *MxJobStatus) AppendRemovingDeadMember(name string) {
	reason := fmt.Sprintf("removing dead member %s", name)

	c := MxJobCondition{
		Type:           MxJobConditionRemovingDeadMember,
		Reason:         reason,
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

// SetReadyCondition for mxnet job status
func (cs *MxJobStatus) SetReadyCondition() {
	c := MxJobCondition{
		Type:           MxJobConditionReady,
		TransitionTime: time.Now().Format(time.RFC3339),
	}

	if len(cs.Conditions) == 0 {
		cs.appendCondition(c)
		return
	}

	lastc := cs.Conditions[len(cs.Conditions)-1]
	if lastc.Type == MxJobConditionReady {
		return
	}
	cs.appendCondition(c)
}

func (cs *MxJobStatus) appendCondition(c MxJobCondition) {
	cs.Conditions = append(cs.Conditions, c)
	if len(cs.Conditions) > 10 {
		cs.Conditions = cs.Conditions[1:]
	}
}

func scalingReason(from, to int) string {
	return fmt.Sprintf("Current cluster size: %d, desired cluster size: %d", from, to)
}
