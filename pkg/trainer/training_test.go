package trainer

import (
	"reflect"
	"testing"

	"sync"

	"github.com/deepinsight/mxnet-operator/pkg/spec"
	mxJobFake "github.com/deepinsight/mxnet-operator/pkg/util/k8sutil/fake"
	"github.com/gogo/protobuf/proto"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/pkg/api/v1"
)

func TestIsRetryableTerminationState(t *testing.T) {
	type TestCase struct {
		State    v1.ContainerStateTerminated
		Expected bool
	}

	cases := []TestCase{
		{
			// Since reason is empty we don't trust the exit code.
			State: v1.ContainerStateTerminated{
				ExitCode: 0,
			},
			Expected: true,
		},
		{
			State: v1.ContainerStateTerminated{
				ExitCode: 0,
				Message:  "some reason",
			},
			Expected: false,
		},
		{
			State: v1.ContainerStateTerminated{
				ExitCode: 1,
				Message:  "some reason",
			},
			Expected: false,
		},
		{
			// Since Reason is empty we don't trust the exit code.
			State: v1.ContainerStateTerminated{
				ExitCode: 1,
			},
			Expected: true,
		},
		{
			State: v1.ContainerStateTerminated{
				ExitCode: 244,
				Message:  "some reason",
			},
			Expected: true,
		},
		{
			State: v1.ContainerStateTerminated{
				ExitCode: 244,
				Reason:   "OOMKilled",
			},
			Expected: false,
		},
	}

	for _, c := range cases {
		actual := isRetryableTerminationState(&c.State)
		if actual != c.Expected {
			t.Errorf("isRetryableTerminationState(%+v)=%v want %v", c.State, actual, c.Expected)
		}
	}
}

func TestClusterSpec(t *testing.T) {
	type TestCase struct {
		Spec     *spec.MxJob
		Expected map[string][]string
	}

	cases := []TestCase{
		{
			Spec: &spec.MxJob{
				Spec: spec.MxJobSpec{
					RuntimeId: "runtime",
					ReplicaSpecs: []*spec.MxReplicaSpec{
						{
							Replicas:   proto.Int32(2),
							PsRootPort: proto.Int32(22),
							Template: &v1.PodTemplateSpec{
								Spec: v1.PodSpec{
									Containers: []v1.Container{
										{
											Name: "tensorflow",
										},
									},
								},
							},
							MxReplicaType: spec.PS,
						},
						{
							Replicas:   proto.Int32(1),
							PsRootPort: proto.Int32(42),
							Template: &v1.PodTemplateSpec{
								Spec: v1.PodSpec{
									Containers: []v1.Container{
										{
											Name: "tensorflow",
										},
									},
								},
							},
							MxReplicaType: spec.MASTER,
						},
						{
							Replicas:   proto.Int32(3),
							PsRootPort: proto.Int32(40),
							Template: &v1.PodTemplateSpec{
								Spec: v1.PodSpec{
									Containers: []v1.Container{
										{
											Name: "tensorflow",
										},
									},
								},
							},
							MxReplicaType: spec.WORKER,
						},
					},
				},
			},

			Expected: map[string][]string{
				"ps":     []string{"ps-runtime-0:22", "ps-runtime-1:22"},
				"master": []string{"master-runtime-0:42"},
				"worker": []string{"worker-runtime-0:40", "worker-runtime-1:40", "worker-runtime-2:40"},
			},
		},
	}

	for _, c := range cases {

		clientSet := fake.NewSimpleClientset()

		stopC := make(chan struct{})

		wg := &sync.WaitGroup{}
		job, err := initJob(clientSet, &mxJobFake.MxJobClientFake{}, c.Spec, stopC, wg)

		if err != nil {
			t.Fatalf("initJob failed: %v", err)
		}

		if err := job.setup(&spec.ControllerConfig{}); err != nil {
			t.Fatalf("job.setup() failed: %v", err)
		}

		actual := job.ClusterSpec()

		for k, v := range c.Expected {
			actualV, ok := actual[k]
			if !ok {
				t.Errorf("Actual cluster spec is missing key: %v", k)
				continue
			}
			if !reflect.DeepEqual(actualV, v) {
				t.Errorf("Key %v got %v want %v", k, actualV, v)
			}
		}
	}
}

func TestJobSetup(t *testing.T) {
	// Verify the setup will fill in the RuntimeId.
	clientSet := fake.NewSimpleClientset()

	type testCase struct {
		jobSpec      *spec.MxJob
		expectMounts int
	}

	testCases := []testCase{
		{
			jobSpec: &spec.MxJob{
				Spec: spec.MxJobSpec{
					ReplicaSpecs: []*spec.MxReplicaSpec{
						{
							Replicas:   proto.Int32(2),
							PsRootPort: proto.Int32(10),
							Template: &v1.PodTemplateSpec{
								Spec: v1.PodSpec{
									Containers: []v1.Container{
										{
											Name: "tensorflow",
										},
									},
								},
							},
							MxReplicaType: spec.PS,
						},
					},
				},
			},
			expectMounts: 0,
		},
		{
			jobSpec: &spec.MxJob{
				Spec: spec.MxJobSpec{
					ReplicaSpecs: []*spec.MxReplicaSpec{
						{
							Replicas:   proto.Int32(2),
							PsRootPort: proto.Int32(10),
							Template: &v1.PodTemplateSpec{
								Spec: v1.PodSpec{
									Containers: []v1.Container{
										{
											Name: "tensorflow",
											Resources: v1.ResourceRequirements{
												Requests: map[v1.ResourceName]resource.Quantity{
													"nvidia-gpu": resource.MustParse("1"),
												},
											},
										},
									},
								},
							},
							MxReplicaType: spec.PS,
						},
					},
				},
			},
			expectMounts: 1,
		},
	}

	config := &spec.ControllerConfig{
		Accelerators: map[string]spec.AcceleratorConfig{
			"nvidia-gpu": spec.AcceleratorConfig{
				Volumes: []spec.AcceleratorVolume{
					{
						Name:      "cuda-lib",
						HostPath:  "/home/cuda",
						MountPath: "/usr/local/cuda",
					},
				},
			},
		},
	}

	for _, c := range testCases {
		stopC := make(chan struct{})
		wg := &sync.WaitGroup{}
		job, err := initJob(clientSet, &mxJobFake.MxJobClientFake{}, c.jobSpec, stopC, wg)

		err = job.setup(config)

		if err != nil {
			t.Errorf("j.setup error: %v", err)
		}

		// Make sure the runtime id is set.
		if job.job.Spec.RuntimeId == "" {
			t.Errorf("RuntimeId should not be empty after calling setup.")
		}

		if len(job.job.Spec.ReplicaSpecs[0].Template.Spec.Volumes) != c.expectMounts {
			t.Errorf("Expect %v Volumes got %v", c.expectMounts, len(job.job.Spec.ReplicaSpecs[0].Template.Spec.Volumes))
		}

		if len(job.job.Spec.ReplicaSpecs[0].Template.Spec.Containers[0].VolumeMounts) != c.expectMounts {
			t.Errorf("Expect %v VolumeMounts got %v", c.expectMounts, len(job.job.Spec.ReplicaSpecs[0].Template.Spec.Containers[0].VolumeMounts))
		}
	}
}
