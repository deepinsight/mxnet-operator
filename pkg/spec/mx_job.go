package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deepinsight/mxnet-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
	//"github.com/golang/protobuf/proto"
	"github.com/gogo/protobuf/proto"
)

const (
	CRDKind       = "MxJob"
	CRDKindPlural = "mxjobs"
	CRDGroup      = "mlkube.io"
	CRDVersion    = "v1beta1"
	CRDApiVersion = CRDGroup + "/" + CRDVersion // "mlkube.io/v1beta1"

	// Value of the APP label that gets applied to a lot of entities.
	AppLabel = "mxnet-job"

	// Defaults for the Spec
	PS_ROOT_PORT = 9091
	Replicas     = 1
)

func CRDName() string {
	return fmt.Sprintf("%s.%s", CRDKindPlural, CRDGroup)
}

type MxJob struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec            MxJobSpec         `json:"spec"`
	Status          MxJobStatus       `json:"status"`
}

func (c *MxJob) AsOwner() metav1.OwnerReference {
	trueVar := true
	// TODO: In 1.6 this is gonna be "k8s.io/kubernetes/pkg/apis/meta/v1"
	// Both api.OwnerReference and metatypes.OwnerReference are combined into that.
	return metav1.OwnerReference{
		APIVersion: c.APIVersion,
		Kind:       c.Kind,
		Name:       c.Metadata.Name,
		UID:        c.Metadata.UID,
		Controller: &trueVar,
	}
}

// Key() is an unique key for MxJob to store in maps
func (j *MxJob) Key() string {
	return j.Metadata.Namespace + "-" + j.Metadata.Name
}

// TODO(jlewi): Need to define the actual configuration for the MXNet MxJob.
type MxJobSpec struct {
	// TODO(jlewi): Can we we get rid of this and use some value from Kubernetes or a random ide.
	RuntimeId string

	// ReplicaSpecs specifies the Mx replicas to run.
	ReplicaSpecs []*MxReplicaSpec `json:"replicaSpecs"`
}

// MxReplicaType determines how a set of Mx processes are handled.
type MxReplicaType string

const (
	SCHEDULER MxReplicaType = "SCHEDULER"
	SERVER    MxReplicaType = "SERVER"
	WORKER    MxReplicaType = "WORKER"
)

// ContainerName is an enum for expected containers.
type ContainerName string

const (
	MXNET ContainerName = "mxnet"
)

// TODO(jlewi): We probably want to add a name field. This would allow us to have more than 1 type of each worker.
// This might be useful if you wanted to have a separate set of workers to do eval.
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
	MxReplicaType `json:"MxReplicaType"`
}

// Validate checks that the MxJobSpec is valid.
func (c *MxJobSpec) Validate() error {
	// Check that each replica has a MXNet container.
	for _, r := range c.ReplicaSpecs {
		found := false
		if r.Template == nil {
			return fmt.Errorf("Replica is missing Template; %v", util.Pformat(r))
		}

		if r.MxReplicaType == WORKER && *r.Replicas > 1 {
			if r.MxReplicaType == SCHEDULER && *r.Replicas != 1 {
				return errors.New("For distributed training, the SCHEDULER must have Replicas = 1")
			}
			if r.PsRootPort == nil {
				return errors.New("For distributed training, MxReplicaSpec.PsRootPort can't be nil.")
			}
		}

		// Make sure the replica type is valid.
		validReplicaTypes := []MxReplicaType{SCHEDULER, SERVER, WORKER}

		isValidReplicaType := false
		for _, t := range validReplicaTypes {
			if t == r.MxReplicaType {
				isValidReplicaType = true
				break
			}
		}

		if !isValidReplicaType {
			return fmt.Errorf("MxReplicaSpec.MxReplicaType is %v but must be one of %v", r.MxReplicaType, validReplicaTypes)
		}

		for _, c := range r.Template.Spec.Containers {
			if c.Name == string(MXNET) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Replica type %v is missing a container named %v", r.MxReplicaType, MXNET)
		}
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
			r.PsRootPort = proto.Int32(PS_ROOT_PORT)
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

type MxJobPhase string

const (
	MxJobPhaseNone     MxJobPhase = ""
	MxJobPhaseCreating            = "Creating"
	MxJobPhaseRunning             = "Running"
	MxJobPhaseCleanUp             = "CleanUp"
	MxJobPhaseFailed              = "Failed"
	MxJobPhaseDone                = "Done"
)

type MxJobCondition struct {
	Type MxJobConditionType `json:"type"`

	Reason string `json:"reason"`

	TransitionTime string `json:"transitionTime"`
}

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

type State string

const (
	StateUnknown   State = "Unknown"
	StateRunning   State = "Running"
	StateSucceeded State = "Succeeded"
	StateFailed    State = "Failed"
)

type MxJobStatus struct {
	// Phase is the MxJob running phase
	Phase  MxJobPhase `json:"phase"`
	Reason string     `json:"reason"`

	// ControlPuased indicates the operator pauses the control of the cluster.
	// TODO(jlewi): I think we can get rid of ControlPaued.
	ControlPaused bool `json:"controlPaused"`

	// Condition keeps ten most recent cluster conditions
	Conditions []MxJobCondition `json:"conditions"`

	// State indicates the state of the job.
	State State `json:"state"`

	// ReplicaStatuses specifies the status of each Mx replica.
	ReplicaStatuses []*MxReplicaStatus `json:"replicaStatuses"`
}

type ReplicaState string

const (
	ReplicaStateUnknown   ReplicaState = "Unknown"
	ReplicaStateStarting               = "Starting"
	ReplicaStateRunning                = "Running"
	ReplicaStateFailed                 = "Failed"
	ReplicaStateSucceeded              = "Succeeded"
)

type MxReplicaStatus struct {
	MxReplicaType `json:"Mx_replica_type"`
	// State is the overall state of the replica
	State ReplicaState `json:"state"`

	// ReplicasStates provides the number of replicas in each status.
	ReplicasStates map[ReplicaState]int
}

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

func (cs *MxJobStatus) IsFailed() bool {
	if cs == nil {
		return false
	}
	return cs.State == StateFailed
}

func (cs *MxJobStatus) SetPhase(p MxJobPhase) {
	cs.Phase = p
}

func (cs *MxJobStatus) PauseControl() {
	cs.ControlPaused = true
}

func (cs *MxJobStatus) Control() {
	cs.ControlPaused = false
}

func (cs *MxJobStatus) SetReason(r string) {
	cs.Reason = r
}

func (cs *MxJobStatus) SetState(s State) {
	cs.State = s
}

// TODO(jlewi): Get rid of the append methods that we don't need
func (cs *MxJobStatus) AppendScalingDownCondition(from, to int) {
	c := MxJobCondition{
		Type:           MxJobConditionScalingDown,
		Reason:         scalingReason(from, to),
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

func (cs *MxJobStatus) AppendRecoveringCondition() {
	c := MxJobCondition{
		Type:           MxJobConditionRecovering,
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

func (cs *MxJobStatus) AppendUpgradingCondition(to string, member string) {
	reason := fmt.Sprintf("upgrading cluster member %s version to %v", member, to)

	c := MxJobCondition{
		Type:           MxJobConditionUpgrading,
		Reason:         reason,
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

func (cs *MxJobStatus) AppendRemovingDeadMember(name string) {
	reason := fmt.Sprintf("removing dead member %s", name)

	c := MxJobCondition{
		Type:           MxJobConditionRemovingDeadMember,
		Reason:         reason,
		TransitionTime: time.Now().Format(time.RFC3339),
	}
	cs.appendCondition(c)
}

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
