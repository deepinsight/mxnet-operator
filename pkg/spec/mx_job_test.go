package spec

import (
	"reflect"
	"testing"

	"github.com/deepinsight/mxnet-operator/pkg/util"
	"github.com/gogo/protobuf/proto"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/pkg/api/v1"
)

func TestAddAccelertor(t *testing.T) {
	type testCase struct {
		in       *MxJobSpec
		expected *MxJobSpec
		config   map[string]AcceleratorConfig
	}

	testCases := []testCase{
		// Case 1 checks that we look at requests.
		{
			in: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Replicas:   proto.Int32(2),
						PsRootPort: proto.Int32(10),
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
										Resources: v1.ResourceRequirements{
											Requests: map[v1.ResourceName]resource.Quantity{
												"nvidia-gpu": resource.MustParse("1"),
											},
										},
									},
								},
							},
						},
						MxReplicaType: SERVER,
					},
				},
			},
			expected: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Replicas:   proto.Int32(2),
						PsRootPort: proto.Int32(10),
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
										Resources: v1.ResourceRequirements{
											Requests: map[v1.ResourceName]resource.Quantity{
												"nvidia-gpu": resource.MustParse("1"),
											},
										},
										VolumeMounts: []v1.VolumeMount{
											{
												Name:      "cuda-lib",
												MountPath: "/usr/local/cuda",
											},
										},
									},
								},
								Volumes: []v1.Volume{
									{
										Name: "cuda-lib",
										VolumeSource: v1.VolumeSource{
											HostPath: &v1.HostPathVolumeSource{
												Path: "/home/cuda",
											},
										},
									},
								},
							},
						},
						MxReplicaType: SERVER,
					},
				},
			},
			config: map[string]AcceleratorConfig{
				"nvidia-gpu": AcceleratorConfig{
					Volumes: []AcceleratorVolume{
						{
							Name:      "cuda-lib",
							HostPath:  "/home/cuda",
							MountPath: "/usr/local/cuda",
						},
					},
				},
			},
		},
		// Case 2 checks that we look at limit.
		{
			in: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Replicas:   proto.Int32(2),
						PsRootPort: proto.Int32(10),
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
										Resources: v1.ResourceRequirements{
											Limits: map[v1.ResourceName]resource.Quantity{
												"nvidia-gpu": resource.MustParse("1"),
											},
										},
									},
								},
							},
						},
						MxReplicaType: SERVER,
					},
				},
			},
			expected: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Replicas:   proto.Int32(2),
						PsRootPort: proto.Int32(10),
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
										Resources: v1.ResourceRequirements{
											Limits: map[v1.ResourceName]resource.Quantity{
												"nvidia-gpu": resource.MustParse("1"),
											},
										},
										VolumeMounts: []v1.VolumeMount{
											{
												Name:      "cuda-lib",
												MountPath: "/usr/local/cuda",
											},
										},
									},
								},
								Volumes: []v1.Volume{
									{
										Name: "cuda-lib",
										VolumeSource: v1.VolumeSource{
											HostPath: &v1.HostPathVolumeSource{
												Path: "/home/cuda",
											},
										},
									},
								},
							},
						},
						MxReplicaType: SERVER,
					},
				},
			},
			config: map[string]AcceleratorConfig{
				"nvidia-gpu": AcceleratorConfig{
					Volumes: []AcceleratorVolume{
						{
							Name:      "cuda-lib",
							HostPath:  "/home/cuda",
							MountPath: "/usr/local/cuda",
						},
					},
				},
			},
		},
		// Case 3 no GPUs
		{
			in: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Replicas:   proto.Int32(2),
						PsRootPort: proto.Int32(10),
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
									},
								},
							},
						},
						MxReplicaType: SERVER,
					},
				},
			},
			expected: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Replicas:   proto.Int32(2),
						PsRootPort: proto.Int32(10),
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
									},
								},
							},
						},
						MxReplicaType: SERVER,
					},
				},
			},
			config: map[string]AcceleratorConfig{
				"nvidia-gpu": AcceleratorConfig{
					Volumes: []AcceleratorVolume{
						{
							Name:      "cuda-lib",
							HostPath:  "/home/cuda",
							MountPath: "/usr/local/cuda",
						},
					},
				},
			},
		},
	}

	for _, c := range testCases {
		if err := c.in.ConfigureAccelerators(c.config); err != nil {
			t.Errorf("ConfigureAccelerators error; %v", err)
		}
		if !reflect.DeepEqual(c.in, c.expected) {
			t.Errorf("Want\n%v; Got\n %v", util.Pformat(c.expected), util.Pformat(c.in))
		}
	}
}

func TestSetDefaults(t *testing.T) {
	type testCase struct {
		in       *MxJobSpec
		expected *MxJobSpec
	}

	testCases := []testCase{
		{
			in: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
									},
								},
							},
						},
					},
				},
			},
			expected: &MxJobSpec{
				ReplicaSpecs: []*MxReplicaSpec{
					{
						Replicas:   proto.Int32(1),
						PsRootPort: proto.Int32(9091),
						Template: &v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "mxnet",
									},
								},
							},
						},
						MxReplicaType: WORKER,
					},
				},
			},
		},
	}

	for _, c := range testCases {
		if err := c.in.SetDefaults(); err != nil {
			t.Errorf("SetDefaults error; %v", err)
		}
		if !reflect.DeepEqual(c.in, c.expected) {
			t.Errorf("Want\n%v; Got\n %v", util.Pformat(c.expected), util.Pformat(c.in))
		}
	}
}
