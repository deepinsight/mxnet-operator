// training is a package for managing MXNet training jobs.
package trainer

import (
	"fmt"

	"reflect"

	"github.com/deepinsight/mxnet-operator/pkg/spec"
	"github.com/deepinsight/mxnet-operator/pkg/util"
	"github.com/deepinsight/mxnet-operator/pkg/util/k8sutil"
	"github.com/deepinsight/mxnet-operator/pkg/util/retryutil"
	log "github.com/golang/glog"

	"math"
	"sync"
	"time"

	"github.com/deepinsight/mxnet-operator/pkg/garbagecollection"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
)

const (
	NAMESPACE string = "default"
)

var (
	reconcileInterval = 8 * time.Second
)

type jobEventType string

const (
	eventDeleteJob jobEventType = "Delete"
	eventModifyJob jobEventType = "Modify"
)

type jobEvent struct {
	typ jobEventType
	// TODO(jlewi): Rename cluster to job.
	cluster *spec.MxJob
}

// TODO(jlewi): We should switch a New pattern and make trainingJob private so we can
// ensure correctness on creation.
type TrainingJob struct {
	job *spec.MxJob

	KubeCli kubernetes.Interface

	Replicas []*MXReplicaSet

	mxJobClient k8sutil.MxJobClient

	// in memory state of the job.
	// status is the source of truth after job struct is materialized. Changes to the status to be persisted
	// should be made here.
	status spec.MxJobStatus

	memberCounter int

	// eventCh is used to provide Kubernetes events for a particular cluster that need to be handled.
	eventCh chan *jobEvent

	// stopCh is a channel used to communicate that the cluster needs to be stopped.
	stopCh chan struct{}

	gc *garbagecollection.GC
}

func initJob(kubeCli kubernetes.Interface, mxJobClient k8sutil.MxJobClient, job *spec.MxJob, stopC <-chan struct{}, wg *sync.WaitGroup) (*TrainingJob, error) {
	j := &TrainingJob{
		KubeCli:     kubeCli,
		mxJobClient: mxJobClient,
		Replicas:    make([]*MXReplicaSet, 0),
		job:         job,
		eventCh:     make(chan *jobEvent, 100),
		stopCh:      make(chan struct{}),
		status:      job.Status.Copy(),
		gc:          garbagecollection.New(kubeCli, mxJobClient, job.Metadata.Namespace),
	}

	return j, nil
}

func NewJob(kubeCli kubernetes.Interface, mxJobClient k8sutil.MxJobClient, mxjob *spec.MxJob, stopC <-chan struct{}, wg *sync.WaitGroup, config *spec.ControllerConfig) (*TrainingJob, error) {
	j, err := initJob(kubeCli, mxJobClient, mxjob, stopC, wg)
	if err != nil {
		return nil, err
	}
	// Increment the wait group which the controller uses to monitor the job processing.
	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := j.setup(config); err != nil {
			log.Errorf("MxJob failed to setup: %v", err)
			if j.status.Phase != spec.MxJobPhaseFailed {
				j.status.SetReason(err.Error())
				j.status.SetPhase(spec.MxJobPhaseFailed)
				if err := j.updateTPRStatus(); err != nil {
					log.Errorf("failed to update cluster phase (%v): %v", spec.MxJobPhaseFailed, err)
				}
			}
			return
		}
		j.run(stopC)
	}()

	return j, nil
}

// createResources creates all the replicas
func (j *TrainingJob) createResources() error {
	for _, r := range j.Replicas {
		if err := r.Create(); err != nil {
			return err
		}
	}

	return nil
}

// deleteResources deletes the replicas
func (j *TrainingJob) deleteResources() error {
	for _, r := range j.Replicas {
		if err := r.Delete(); err != nil {
			return err
		}
	}

	return nil
}

// TODO(jlewi): We can probably delete this.

//func replicaSetStatusToProto(r *MXReplicaSet, status *MXReplicaSetStatus) *tpb.MXReplicaSetStatus {
//
//	p := &tpb.MXReplicaSetStatus{
//		State: status.State.Enum(),
//		// Type: r.Spec.MxReplicaTypeProcess.Type,
//		ReplicaStates: make([]*tpb.MXReplicaSetStatus_ReplicaStates, 0),
//	}
//
//	for state, count := range status.ReplicasStates {
//		p.ReplicaStates = append(p.ReplicaStates, &tpb.MXReplicaSetStatus_ReplicaStates{
//			State: state.Enum(),
//			NumReplicas: proto.Int(count),
//		})
//	}
//	return p
//}

func (j *TrainingJob) GetStatus() (spec.State, []*spec.MxReplicaStatus, error) {
	state := spec.StateUnknown
	replicaStatuses := make([]*spec.MxReplicaStatus, 0)

	// The state for each replica.
	// TODO(jlewi): We will need to modify this code if we want to allow multiples of a given type of replica.
	replicaSetStates := make(map[spec.MxReplicaType]spec.ReplicaState)

	for _, r := range j.Replicas {
		rStatus, err := r.GetStatus()
		if err != nil {
			log.Errorf("GetStatus() for %v returned error; %v", r.Spec.MxReplicaType, err)
		}

		replicaSetStates[r.Spec.MxReplicaType] = rStatus.State

		replicaStatuses = append(replicaStatuses, &rStatus)

		// If any replicas are failed mark job as failed.
		if rStatus.State == spec.ReplicaStateFailed {
			state = spec.StateFailed
		}
	}
	/*
		if v, ok := replicaSetStates[spec.MASTER]; ok && v == spec.ReplicaStateSucceeded {
			state = spec.StateSucceeded
			return state, replicaStatuses, nil
		}

		if v, ok := replicaSetStates[spec.MASTER]; ok && v == spec.ReplicaStateFailed {
			state = spec.StateFailed
			return state, replicaStatuses, nil
		}
	*/
	state = spec.StateRunning
	return state, replicaStatuses, nil
}

// isRetryableTerminationState returns true if a container terminated in a state
// that we consider retryable.
func isRetryableTerminationState(s *v1.ContainerStateTerminated) bool {
	// TODO(jlewi): Need to match logic in
	// https://cs.corp.google.com/piper///depot/google3/cloud/ml/beta/job/training_job_state_util.cc?l=88
	if s.Reason == "OOMKilled" {
		// If the user's process causes an OOM and Docker kills the container,
		// the termination reason of ContainerState will be specified to
		// 'OOMKilled'. In this case, we can't assume this to be a retryable error.
		//
		// This check should happen before checking the termination log, since
		// if the container terminated with an OOM, the termination log may not
		// be written.
		return false
	}

	if s.Message == "" {
		// launcher.sh should produce a termination log message. So if Kubernetes
		// doesn't report a termmination message then we can infer that
		// launcher.sh didn't exit cleanly. For example, the container might
		// have failed to start. We consider this a retryable error regardless
		// of the actual exit code.
		return true
	}

	// TODO(jlewi): Should we use the exit code reported in the termination
	// log message and not the ExitCode reported by the container.

	if s.ExitCode >= 0 && s.ExitCode <= 127 {
		// For the exit_code in [0, 127]:
		//   0 means success,
		//   1 - 127 corresponds to permanent user errors.
		// We don't want to retry for both cases.
		// More info about exit status can be found in:
		// https://www.gnu.org/software/bash/manual/html_node/Exit-Status.html
		return false
	}

	// For the remaining cases that exit_code from workers that doesn't
	// fall into [0, 127]. They can be:
	//   137 corresponds to SIGKILL,
	//   143 corresponds to SIGTERM,
	//   other values that have undefined behavior.
	// We treat them as internal errors for now and all the internal errors
	// will be retired.
	return true
}

func (j *TrainingJob) masterName() string {
	return fmt.Sprintf("master-%v-0", j.job.Spec.RuntimeId)
}

// setup the training job.
func (j *TrainingJob) setup(config *spec.ControllerConfig) error {
	if j.job == nil {
		return fmt.Errorf("job.Spec can't be nil")
	}

	err := j.job.Spec.SetDefaults()
	if err != nil {
		return fmt.Errorf("there was a problem setting defaults for job spec: %v", err)
	}

	err = j.job.Spec.Validate()
	if err != nil {
		return fmt.Errorf("invalid job spec: %v", err)
	}

	for _, t := range j.job.Spec.ReplicaSpecs {
		r, err := NewMXReplicaSet(j.KubeCli, *t, j)
		if err != nil {
			return err
		}
		j.Replicas = append(j.Replicas, r)
	}

	if err := j.job.Spec.ConfigureAccelerators(config.Accelerators); err != nil {
		return fmt.Errorf("ConfigureAccelerators(...) error; %v", err)
	}

	if j.job.Spec.RuntimeId == "" {
		j.job.Spec.RuntimeId = util.RandString(4)
	}

	var shouldCreateCluster bool
	switch j.status.Phase {
	case spec.MxJobPhaseNone:
		shouldCreateCluster = true
		//case spec.MxJobPhaseCreating:
		//	return errCreatedCluster
	case spec.MxJobPhaseRunning:
		shouldCreateCluster = false
	case spec.MxJobPhaseFailed:
		shouldCreateCluster = false
	default:
		return fmt.Errorf("unexpected MxJob phase: %s", j.status.Phase)
	}

	if shouldCreateCluster {
		return j.triggerCreatePhase()
	}
	return nil
}

// triggerCreatePhase sets the phase to MxJobPhaseCreating additional resource creation happens in TrainingJob.run
// TODO(jlewi): Need to reconcile this function copied from the etcd core operator OS code with the pattern
// for the MX job. What exactly do we want to do during the Create job phase? Right now the create method
// is called on each invocation of reconcile in run to ensure all the required resources exist. Maybe there's
// a better way?
func (j *TrainingJob) triggerCreatePhase() error {
	j.status.SetPhase(spec.MxJobPhaseCreating)

	if err := j.updateTPRStatus(); err != nil {
		return fmt.Errorf("cluster create: failed to update MxJob phase (%v): %v", spec.MxJobPhaseCreating, err)
	}
	log.Infof("Creating job: %v with Spec (%#v), Status (%#v)", j.job.Metadata.Name, j.job.Spec, j.job.Status)

	// TODO(jlewi): I think this collects all the existing resources that have the labels indicating
	// they should be owned by this job. Do they get deleted?
	j.gc.CollectJob(j.job.Metadata.Name, j.job.Metadata.UID)

	return nil
}

func (j *TrainingJob) Delete() {
	// Delete doesn't actually delete any resources. It just sends an event which will be processed by the run
	// method.
	j.send(&jobEvent{typ: eventDeleteJob})
}

// TODO(jlewi): This delete function was copied from the etcd-operator. Need to figure out what the right thing to
// do is. Should we be calling deleteReplicas here?
func (j *TrainingJob) delete() {
	j.gc.CollectJob(j.job.Metadata.Name, garbagecollection.NullUID)
}

// TODO(jlewi): This is sending a clusterEvent to the channel. I think these are events
// coming from the cluster code and not k8s events.
func (j *TrainingJob) send(ev *jobEvent) {
	select {
	case j.eventCh <- ev:
		l, ecap := len(j.eventCh), cap(j.eventCh)
		if l > int(float64(ecap)*0.8) {
			log.Warningf("eventCh buffer is almost full [%d/%d]", l, ecap)
		}
	case <-j.stopCh:
	}
}

// Update sends an update event for the job.
func (j *TrainingJob) Update(newJob *spec.MxJob) {
	j.send(&jobEvent{
		typ:     eventModifyJob,
		cluster: newJob,
	})
}

// updateTPRStatus updates the job status based on TraingingJob.status.
func (j *TrainingJob) updateTPRStatus() error {
	// If the status hasn't changed then there's no reason to update the TPR.
	if reflect.DeepEqual(j.job.Status, j.status) {
		return nil
	}

	newJob := j.job
	newJob.Status = j.status
	newJob, err := j.mxJobClient.Update(j.job.Metadata.Namespace, newJob)
	if err != nil {
		return err
	}

	j.job = newJob

	return nil
}

func (j *TrainingJob) run(stopC <-chan struct{}) {
	// TODO(jlewi): What does the run function do?
	clusterFailed := false

	defer func() {
		if clusterFailed {
			j.reportFailedStatus()

			log.Infof("Deleting the failed MxJob")
			j.delete()
		}

		close(j.stopCh)
	}()

	// Update the phase to running.
	j.status.SetPhase(spec.MxJobPhaseRunning)
	if err := j.updateTPRStatus(); err != nil {
		log.Warningf("failed to update TPR status: %v", err)
	}
	log.Infof("start running...")

	var rerr error
	for {
		select {
		case <-stopC:
			return
		case event := <-j.eventCh:
			switch event.typ {

			// TODO(jlewi): We need handle a modify event.
			//case eventModifyCluster:
			//	if isSpecEqual(event.cluster.Spec, j.job.Spec) {
			//		break
			//	}
			case eventDeleteJob:
				// TODO(jlewi): Delete is what should cause us to delete the Pods.
				// we shouldn't delete the pods when the jobs finish because leaving the pods
				// allows us to get the logs from the pods after the job finishes.
				//
				log.Infof("MxJob is deleted by the user")
				// TODO(jlewi): This logic is probably insufficient.
				if j.job.Status.Phase != spec.MxJobPhaseCleanUp {
					j.status.SetPhase(spec.MxJobPhaseCleanUp)
				}

				if cErr := j.deleteResources(); cErr != nil {
					log.Errorf("trainingJob.deleteResources() error; %v", cErr)
				}
				// j.status.SetPhase(spec.MxJobPhaseDone)
				// Return from run because we want to stop reconciling the object.
				return
			}

		case <-time.After(reconcileInterval):
			// TODO(jlewi): Can we determine from the TPR status whether we should
			// Create the resources or not? We need to ensure the resources exist so for
			// now we always call Create.
			if j.job.Status.Phase == spec.MxJobPhaseRunning {
				// We call Create to make sure all the resources exist and are running.
				if cErr := j.createResources(); cErr != nil {
					log.Errorf("trainingJobCreateReplicas() error; %v", cErr)
				}

				state, replicaStatuses, err := j.GetStatus()

				j.status.ReplicaStatuses = replicaStatuses
				if err != nil {
					log.Errorf("GetStatus() for job %v returned error: %v", j.job.Metadata.Name, err)
				}
				// TODO(jlewi): We should update the Phase if we detect the job is done.
				if state == spec.StateFailed {
					log.Errorf("Master failed Job: %v.", j.job.Metadata.Name)
					j.status.SetPhase(spec.MxJobPhaseDone)
					j.status.SetState(spec.StateFailed)
				} else if state == spec.StateSucceeded {
					log.Infof("Master succeeded Job: %v.", j.job.Metadata.Name)
					j.status.SetPhase(spec.MxJobPhaseDone)
					j.status.SetState(spec.StateSucceeded)
				} else {
					log.V(1).Infof("Job %v status=%v", j.job.Metadata.Name, util.Pformat(j.status))
				}
			}

			// If the phase changed we should update the TPR.
			if err := j.updateTPRStatus(); err != nil {
				log.Warningf("Job %v, failed to update TPR status error: %v", j.job.Metadata.Name, err)
			}

			if j.job.Status.Phase == spec.MxJobPhaseCleanUp {
				if cErr := j.deleteResources(); cErr != nil {
					log.Errorf("Job %v trainingJob.Delete() error; %v", j.job.Metadata.Name, cErr)
				}
				// j.status.SetPhase(spec.MxJobPhaseDone)
				// Return from run because we want to stop reconciling the object.
				return
			}

			if rerr != nil {
				log.Errorf("failed to reconcile job %v, error: %v", j.job.Metadata.Name, rerr)
				break
			}

			// updateTPRStatus will update the status of the TPR with c.Status if c.Status
			// doesn't match c.Cluster.status. So you can chang c.Status in order to propogate
			// changes to the TPR status.
			if err := j.updateTPRStatus(); err != nil {
				log.Warningf("Job %v; failed to update TPR status error: %v", j.job.Metadata.Name, err)
			}
		}

		//if isFatalError(rerr) {
		//	clusterFailed = true
		//	j.status.SetReason(rerr.Error())
		//
		//	log.Errorf("cluster failed: %v", rerr)
		//	return
		//}
	}
}

//func isSpecEqual(s1, s2 spec.MxJobSpec) bool {
//	// TODO(jlewi): Need to implement this function.
//	return false
//	//if s1.Size != s2.Size || s1.Paused != s2.Paused || s1.Version != s2.Version {
//	//	return false
//	//}
//	//return isBackupPolicyEqual(s1.Backup, s2.Backup)
//}

// TODO(jlewi): We probably need to update this function.
func (j *TrainingJob) reportFailedStatus() {
	retryInterval := 5 * time.Second

	f := func() (bool, error) {
		j.status.SetPhase(spec.MxJobPhaseFailed)
		err := j.updateTPRStatus()
		if err == nil || k8sutil.IsKubernetesResourceNotFoundError(err) {
			return true, nil
		}

		if !apierrors.IsConflict(err) {
			log.Warningf("retry report status in %v: fail to update: %v", retryInterval, err)
			return false, nil
		}

		cl, err := j.mxJobClient.Get(j.job.Metadata.Namespace, j.job.Metadata.Name)
		if err != nil {
			// Update (PUT) will return conflict even if object is deleted since we have UID set in object.
			// Because it will check UID first and return something like:
			// "Precondition failed: UID in precondition: 0xc42712c0f0, UID in object meta: ".
			if k8sutil.IsKubernetesResourceNotFoundError(err) {
				return true, nil
			}
			log.Warningf("retry report status in %v: fail to get latest version: %v", retryInterval, err)
			return false, nil
		}
		j.job = cl
		return false, nil

	}

	retryutil.Retry(retryInterval, math.MaxInt64, f)
}

func (j *TrainingJob) name() string {
	return j.job.Metadata.GetName()
}
