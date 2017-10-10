package trainer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/deepinsight/mxnet-operator/pkg/spec"

	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	// TOOO(jlewi): Rename to apiErrors
	"github.com/deepinsight/mxnet-operator/pkg/util"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sErrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	batch "k8s.io/client-go/pkg/apis/batch/v1"
)

// MXReplicaSet is a set of MX processes all acting as the same role (e.g. worker
type MXReplicaSet struct {
	ClientSet kubernetes.Interface
	// Job is a pointer to the TrainingJob to which this replica belongs.
	Job  *TrainingJob
	Spec spec.MxReplicaSpec
}

// MXReplicas is an interface for managing a set of replicas.
type MXReplicaSetInterface interface {
	Create() error
	Delete() error
	GetStatus() (spec.MxReplicaStatus, error)
}

// MXConfig is a struct representing the MXNET config. This struct is turned into an environment
// which is used by MXNET processes to configure themselves.
type MxConfig struct {
	Task map[string]interface{} `json:"task"`
}

func NewMXReplicaSet(clientSet kubernetes.Interface, mxReplicaSpec spec.MxReplicaSpec, job *TrainingJob) (*MXReplicaSet, error) {
	if mxReplicaSpec.MxReplicaType == spec.SCHEDULER && *mxReplicaSpec.Replicas != 1 {
		return nil, errors.New("The SCHEDULER must have Replicas = 1")
	}

	if mxReplicaSpec.MxReplicaType == spec.SCHEDULER {
		if mxReplicaSpec.PsRootPort == nil {
			return nil, errors.New("mxReplicaSpec.PsRootPort can't be nil.")
		}
	}

	if mxReplicaSpec.Template == nil {
		return nil, errors.New("mxReplicaSpec.Template can't be nil.")
	}

	// Make sure the replica type is valid.
	validReplicaTypes := []spec.MxReplicaType{spec.SCHEDULER, spec.SERVER, spec.WORKER}

	isValidReplicaType := false
	for _, t := range validReplicaTypes {
		if t == mxReplicaSpec.MxReplicaType {
			isValidReplicaType = true
			break
		}
	}

	if !isValidReplicaType {
		return nil, fmt.Errorf("mxReplicaSpec.MxReplicaType is %v but must be one of %v", mxReplicaSpec.MxReplicaType, validReplicaTypes)
	}
	return &MXReplicaSet{
		ClientSet: clientSet,
		Job:       job,
		Spec:      mxReplicaSpec,
	}, nil
}

// Labels returns the labels for this replica set.
func (s *MXReplicaSet) Labels() KubernetesLabels {
	return KubernetesLabels(map[string]string{
		"mxnet.mlkube.io": "",
		"job_type":        string(s.Spec.MxReplicaType),
		// runtime_id is set by Job.setup, which is called after the MxReplicaSet is created.
		// this is why labels aren't a member variable.
		"runtime_id": s.Job.job.Spec.RuntimeId})
}

func (s *MXReplicaSet) Create() error {
	if s.Job.job.Spec.JobMode == spec.LocalJob {
		return s.createLocal()
	} else if s.Job.job.Spec.JobMode == spec.DistJob {
		return s.createDist()
	}
	return nil
}

func (s *MXReplicaSet) createLocal() error {

	newPodSpecTemplate := *s.Spec.Template
	taskLabels := s.Labels()
	taskLabels["task_index"] = fmt.Sprintf("%v", 0)
	newJ := &batch.Job{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:   s.jobName(0),
			Labels: taskLabels,
		},
		Spec: batch.JobSpec{
			Completions: proto.Int32(1),
			Parallelism: proto.Int32(1),
			Template:    newPodSpecTemplate,
		},
	}

	if newJ.Spec.Template.ObjectMeta.Labels == nil {
		newJ.Spec.Template.ObjectMeta.Labels = make(map[string]string)
	}

	// Pods need to be tagged with the labels.
	for k, v := range taskLabels {
		newJ.Spec.Template.ObjectMeta.Labels[k] = v
	}

	log.Infof("Creating Job: %v", newJ.ObjectMeta.Name)
	_, err := s.ClientSet.BatchV1().Jobs(s.Job.job.Metadata.Namespace).Create(newJ)

	// If the job already exists do nothing.
	if err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			log.Infof("%v already exists.", s.jobName(0))

		} else {
			return k8sErrors.NewAggregate([]error{fmt.Errorf("Creating Job %v returned error.", newJ.ObjectMeta.Name), err})
		}
	}
	return nil
}

func (s *MXReplicaSet) createDist() error {
	for index := int32(0); index < *s.Spec.Replicas; index++ {
		taskLabels := s.Labels()
		taskLabels["task_index"] = fmt.Sprintf("%v", index)

		if s.Spec.MxReplicaType == spec.SCHEDULER {
			// Create the service.
			service := &v1.Service{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:   s.jobName(index),
					Labels: taskLabels,
				},
				Spec: v1.ServiceSpec{
					Selector: taskLabels,
					Ports: []v1.ServicePort{
						{
							Name: "ps-root-port",
							Port: *s.Spec.PsRootPort,
						},
					},
				},
			}

			log.Infof("Creating Service: %v", service.ObjectMeta.Name)
			_, err := s.ClientSet.CoreV1().Services(s.Job.job.Metadata.Namespace).Create(service)
			// If the job already exists do nothing.
			if err != nil {
				if k8s_errors.IsAlreadyExists(err) {
					log.Infof("Service %v already exists.", s.jobName(index))
				} else {
					return k8sErrors.NewAggregate([]error{fmt.Errorf("Creating service %v returned error.", service.ObjectMeta.Name), err})
				}
			}
		}

		// Configure the MXCONFIG environment variable.
		//
		// TODO(jlewi): We would need to add support for hyperparameter jobs to support CMLE
		// hyperparameter tuning.

		// Make a copy of the template because we will modify it below.
		// TODO(jlewi): I don't fully understand why this works but setting Template: *s.Spec.Template
		// leads to MX_CONFIG being added multiples as an environment variable.
		newPodSpecTemplate := *s.Spec.Template
		// TODO(jlewi): We need to set environment variable MX_CONFIG.
		newJ := &batch.Job{
			ObjectMeta: meta_v1.ObjectMeta{
				Name:   s.jobName(index),
				Labels: taskLabels,
			},
			Spec: batch.JobSpec{
				Completions: proto.Int32(1),
				Parallelism: proto.Int32(1),
				Template:    newPodSpecTemplate,
			},
		}

		if newJ.Spec.Template.ObjectMeta.Labels == nil {
			newJ.Spec.Template.ObjectMeta.Labels = make(map[string]string)
		}

		// Pods need to be tagged with the labels.
		for k, v := range taskLabels {
			newJ.Spec.Template.ObjectMeta.Labels[k] = v
		}

		// Add MXNet environment variable.
		for i, _ := range newJ.Spec.Template.Spec.Containers {
			// We can't get c in the loop variable because that would be by value so our modifications
			// wouldn't have any effect.
			c := &newJ.Spec.Template.Spec.Containers[i]
			if spec.ContainerName(c.Name) != spec.MXNET {
				continue
			}
			if len(c.Env) == 0 {
				c.Env = make([]v1.EnvVar, 5)
			}
			for _, r := range s.Job.job.Spec.ReplicaSpecs {
				switch r.MxReplicaType {
				case spec.SCHEDULER:
					c.Env[0].Name = "DMLC_PS_ROOT_PORT"
					c.Env[0].Value = strconv.Itoa(int(*r.PsRootPort))
					c.Env[1].Name = "DMLC_PS_ROOT_URI"
					c.Env[1].Value = fmt.Sprintf("%v-%v-%v-%v", s.Job.job.Metadata.Name, strings.ToLower(string(r.MxReplicaType)), s.Job.job.Spec.RuntimeId, 0)
				case spec.SERVER:
					c.Env[2].Name = "DMLC_NUM_SERVER"
					c.Env[2].Value = strconv.Itoa(int(*r.Replicas))
				case spec.WORKER:
					c.Env[3].Name = "DMLC_NUM_WORKER"
					c.Env[3].Value = strconv.Itoa(int(*r.Replicas))
				}
			}
			c.Env[4].Name = "DMLC_ROLE"
			c.Env[4].Value = strings.ToLower(string(s.Spec.MxReplicaType))
		}

		log.Infof("Creating Job: %v", newJ.ObjectMeta.Name)
		_, err := s.ClientSet.BatchV1().Jobs(s.Job.job.Metadata.Namespace).Create(newJ)

		// If the job already exists do nothing.
		if err != nil {
			if k8s_errors.IsAlreadyExists(err) {
				log.Infof("%v already exists.", s.jobName(index))

			} else {
				return k8sErrors.NewAggregate([]error{fmt.Errorf("Creating Job %v returned error.", newJ.ObjectMeta.Name), err})
			}
		}
	}
	return nil
}

// Delete deletes the replicas
func (s *MXReplicaSet) Delete() error {
	selector, err := s.Labels().ToSelector()
	if err != nil {
		return err
	}

	failures := false

	options := meta_v1.ListOptions{
		LabelSelector: selector,
	}

	err = s.ClientSet.BatchV1().Jobs(s.Job.job.Metadata.Namespace).DeleteCollection(&meta_v1.DeleteOptions{}, options)

	if err != nil {
		log.Errorf("There was a problem deleting the jobs; %v", err)
		failures = true
	}

	// We need to delete the completed pods.
	err = s.ClientSet.CoreV1().Pods(s.Job.job.Metadata.Namespace).DeleteCollection(&meta_v1.DeleteOptions{}, options)

	if err != nil {
		log.Errorf("There was a problem deleting the pods; %v", err)
		failures = true
	}

	// Services doesn't support DeleteCollection so we delete them individually.
	if s.Spec.MxReplicaType == spec.SCHEDULER {
		err = s.ClientSet.CoreV1().Services(s.Job.job.Metadata.Namespace).Delete(s.jobName(0), &meta_v1.DeleteOptions{})

		if err != nil {
			log.Errorf("Error deleting service %v; %v", s.jobName(0), err)
			failures = true
		}
	}

	if failures {
		return errors.New("Some of the replicas resources could not be deleted")
	}
	return nil
}

// replicaStatusFromPodList returns a status from a list of pods for a job.
func replicaStatusFromPodList(l v1.PodList, name spec.ContainerName) spec.ReplicaState {
	log.V(1).Infof("Get replicaStatus from PodList: %v", util.Pformat(l))
	var latest *v1.Pod
	for _, i := range l.Items {
		if latest == nil {
			latest = &i
			continue
		}
		if latest.Status.StartTime.Before(*i.Status.StartTime) {
			latest = &i
		}
	}

	if latest == nil {
		return spec.ReplicaStateRunning
	}

	var mxState v1.ContainerState

	for _, i := range latest.Status.ContainerStatuses {
		if i.Name != string(name) {
			continue
		}

		// We need to decide whether to use the current state or the previous termination state.
		mxState = i.State

		// If the container previously terminated we will look at the termination to decide whether it is a retryable
		// or permanenent error.
		if i.LastTerminationState.Terminated != nil {
			mxState = i.LastTerminationState
		}
	}

	if mxState.Running != nil || mxState.Waiting != nil {
		return spec.ReplicaStateRunning
	}

	if mxState.Terminated != nil {
		if mxState.Terminated.ExitCode == 0 {
			return spec.ReplicaStateSucceeded
		}

		if isRetryableTerminationState(mxState.Terminated) {
			// Since its a retryable error just return RUNNING.
			// We can just let Kubernetes restart the container to retry.
			return spec.ReplicaStateRunning
		}

		return spec.ReplicaStateFailed
	}

	return spec.ReplicaStateUnknown
}

// Status returns the status of the replica set.
func (s *MXReplicaSet) GetStatus() (spec.MxReplicaStatus, error) {

	status := spec.MxReplicaStatus{
		MxReplicaType:  s.Spec.MxReplicaType,
		State:          spec.ReplicaStateUnknown,
		ReplicasStates: make(map[spec.ReplicaState]int),
	}

	increment := func(state spec.ReplicaState) {
		v, ok := status.ReplicasStates[state]
		if ok {
			status.ReplicasStates[state] = v + 1
		} else {
			status.ReplicasStates[state] = 1
		}
	}

	for index := int32(0); index < *s.Spec.Replicas; index++ {

		j, err := s.ClientSet.BatchV1().Jobs(s.Job.job.Metadata.Namespace).Get(s.jobName(index), meta_v1.GetOptions{})

		if err != nil {
			increment(spec.ReplicaStateUnknown)
			continue
		}

		if j.Status.Succeeded >= 1 {
			increment(spec.ReplicaStateSucceeded)
			continue
		}

		labels := s.Labels()
		labels["task_index"] = fmt.Sprintf("%v", index)
		selector, err := labels.ToSelector()
		if err != nil {
			log.Errorf("labels.ToSelector() error; %v", err)
			increment(spec.ReplicaStateFailed)
			continue
		}

		// TODO(jlewi): Handle errors. We need to get the pod and looking at recent container exits.
		l, err := s.ClientSet.CoreV1().Pods(s.Job.job.Metadata.Namespace).List(meta_v1.ListOptions{
			// TODO(jlewi): Why isn't the label selector working?
			LabelSelector: selector,
		})

		if err != nil {
			// TODO(jlewi): Are there errors that should be treated as retryable errors?
			increment(spec.ReplicaStateFailed)
			continue
		}

		status := replicaStatusFromPodList(*l, spec.MXNET)
		increment(status)
	}

	// Determine the overall status for the replica set based on the status of the individual
	// replicas.
	// If any of the replicas failed mark the set as failed.
	if _, ok := status.ReplicasStates[spec.ReplicaStateFailed]; ok {
		status.State = spec.ReplicaStateFailed
		return status, nil
	}

	// If any replicas are RUNNING mark it as RUNNING.
	if _, ok := status.ReplicasStates[spec.ReplicaStateRunning]; ok {
		status.State = spec.ReplicaStateRunning
		return status, nil
	}

	// If all of the replicas succeeded consider it success.
	if v, ok := status.ReplicasStates[spec.ReplicaStateSucceeded]; ok && int32(v) == *s.Spec.Replicas {
		status.State = spec.ReplicaStateSucceeded
		return status, nil
	}

	return status, nil
}

func (s *MXReplicaSet) jobName(index int32) string {
	return fmt.Sprintf("%v-%v-%v-%v", s.Job.job.Metadata.Name, strings.ToLower(string(s.Spec.MxReplicaType)), s.Job.job.Spec.RuntimeId, index)
}
