// e2e provides an E2E test for MxJobs.
//
// The test creates MxJobs and runs various checks to ensure various operations work as intended.
// The test is intended to run as a helm test that ensures the MxJob operator is working correctly.
// Thus, the program returns non-zero exit status on error.
//
// TODO(jlewi): Do we need to make the test output conform to the TAP(https://testanything.org/)
// protocol so we can fit into the K8s dashboard
//
// TODO(https://github.com/jlewi/mlkube.io/issues/21) The E2E test should actually run distributed TensorFlow.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deepinsight/mxnet-operator/pkg/spec"
	"github.com/deepinsight/mxnet-operator/pkg/util"
	"github.com/deepinsight/mxnet-operator/pkg/util/k8sutil"
	"github.com/gogo/protobuf/proto"
	log "github.com/golang/glog"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
)

const (
	Namespace = "default"
)

var (
	image = flag.String("image", "", "The Docker image containing the TF program to run.")
)

func run() error {
	kubeCli := k8sutil.MustNewKubeClient()
	mxJobClient, err := k8sutil.NewMxJobClient()
	if err != nil {
		return err
	}

	name := "e2e-test-job-" + util.RandString(4)

	original := &spec.MxJob{
		Metadata: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"test.mlkube.io": "",
			},
		},
		Spec: spec.MxJobSpec{
			JobMode: "dist",
			ReplicaSpecs: []*spec.MxReplicaSpec{
				{
					Replicas:      proto.Int32(1),
					PsRootPort:    proto.Int32(9091),
					MxReplicaType: spec.SCHEDULER,
					Template: &v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "mxnet",
									Image: *image,
								},
							},
							RestartPolicy: v1.RestartPolicyOnFailure,
						},
					},
				},
				{
					Replicas:      proto.Int32(1),
					MxReplicaType: spec.SERVER,
					Template: &v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "mxnet",
									Image: *image,
								},
							},
							RestartPolicy: v1.RestartPolicyOnFailure,
						},
					},
				},
				{
					Replicas:      proto.Int32(1),
					MxReplicaType: spec.WORKER,
					Template: &v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "mxnet",
									Image: *image,
								},
							},
							RestartPolicy: v1.RestartPolicyOnFailure,
						},
					},
				},
			},
		},
	}

	_, err = mxJobClient.Create(Namespace, original)

	if err != nil {
		log.Errorf("Creating the job failed; %v", err)
		return err
	}

	// Wait for the job to complete for up to 2 minutes.
	var mxJob *spec.MxJob
	for endTime := time.Now().Add(2 * time.Minute); time.Now().Before(endTime); {
		mxJob, err = mxJobClient.Get(Namespace, name)
		if err != nil {
			log.Warningf("There was a problem getting MxJob: %v; error %v", name, err)
		}

		if mxJob.Status.State == spec.StateSucceeded || mxJob.Status.State == spec.StateFailed {
			break
		}
		log.Infof("Waiting for job %v to finish:\n%v", name, util.Pformat(mxJob))
		time.Sleep(5 * time.Second)
	}

	if mxJob == nil {
		return fmt.Errorf("Failed to get MxJob %v", name)
	}

	if mxJob.Status.State != spec.StateSucceeded {
		// TODO(jlewi): Should we clean up the job.
		return fmt.Errorf("MxJob %v did not succeed;\n %v", name, util.Pformat(mxJob))
	}

	if mxJob.Spec.RuntimeId == "" {
		return fmt.Errorf("MxJob %v doesn't have a RuntimeId", name)
	}

	// Loop over each replica and make sure the expected resources were created.
	for _, r := range original.Spec.ReplicaSpecs {
		baseName := strings.ToLower(string(r.MxReplicaType))

		for i := 0; i < int(*r.Replicas); i += 1 {
			jobName := fmt.Sprintf("%v-%v-%v", baseName, mxJob.Spec.RuntimeId, i)

			_, err := kubeCli.BatchV1().Jobs(Namespace).Get(jobName, metav1.GetOptions{})

			if err != nil {
				return fmt.Errorf("Tfob %v did not create Job %v for ReplicaType %v Index %v", name, jobName, r.MxReplicaType, i)
			}
		}
	}

	// Delete the job and make sure all subresources are properly garbage collected.
	if _, err := mxJobClient.Delete(Namespace, name); err != nil {
		log.Fatal("Failed to delete MxJob %v; error %v", name, err)
	}

	// Define sets to keep track of Job controllers corresponding to Replicas
	// that still exist.
	jobs := make(map[string]bool)

	// Loop over each replica and make sure the expected resources are being deleted.
	for _, r := range original.Spec.ReplicaSpecs {
		baseName := strings.ToLower(string(r.MxReplicaType))

		for i := 0; i < int(*r.Replicas); i += 1 {
			jobName := fmt.Sprintf("%v-%v-%v", baseName, mxJob.Spec.RuntimeId, i)

			jobs[jobName] = true
		}
	}

	// Wait for all jobs and deployment to be deleted.
	for endTime := time.Now().Add(5 * time.Minute); time.Now().Before(endTime) && (len(jobs) > 0); {
		for k := range jobs {
			_, err := kubeCli.BatchV1().Jobs(Namespace).Get(k, metav1.GetOptions{})
			if k8s_errors.IsNotFound(err) {
				// Deleting map entry during loop is safe.
				// See: https://stackoverflow.com/questions/23229975/is-it-safe-to-remove-selected-keys-from-golang-map-within-a-range-loop
				delete(jobs, k)
			} else {
				log.Infof("Job %v still exists", k)
			}
		}

		if len(jobs) > 0 {
			time.Sleep(5 * time.Second)
		}
	}

	if len(jobs) > 0 {
		return fmt.Errorf("Not all Job controllers were successfully deleted.")
	}

	return nil
}

func main() {
	flag.Parse()

	if *image == "" {
		log.Fatalf("--image must be provided.")
	}
	err := run()

	// Generate TAP (https://testanything.org/) output
	fmt.Println("1..1")
	if err == nil {
		fmt.Println("ok 1 - Successfully ran MxJob")
	} else {
		fmt.Printf("not ok 1 - Running MxJob failed %v \n", err)
		// Exit with non zero exit code for Helm tests.
		os.Exit(1)
	}
}
