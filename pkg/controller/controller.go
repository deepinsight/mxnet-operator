// package controller provides a Kubernetes controller for a TensorFlow job resource.
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/deepinsight/mxnet-operator/pkg/spec"
	"github.com/deepinsight/mxnet-operator/pkg/trainer"
	"github.com/deepinsight/mxnet-operator/pkg/util/k8sutil"
	"k8s.io/client-go/kubernetes"

	"github.com/deepinsight/mxnet-operator/pkg/util"
	log "github.com/golang/glog"
	v1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sErrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	kwatch "k8s.io/apimachinery/pkg/watch"
)

var (
	ErrVersionOutdated = errors.New("requested version is outdated in apiserver")

	initRetryWaitTime = 30 * time.Second

	// Workaround for watching CRD resource.
	// client-go has encoding issue and we want something more predictable.
	KubeHttpCli *http.Client
	MasterHost  string
)

type Event struct {
	Type   kwatch.EventType
	Object *spec.MxJob
}

type Controller struct {
	Namespace   string
	KubeCli     kubernetes.Interface
	ApiCli      apiextensionsclient.Interface
	MxJobClient k8sutil.MxJobClient

	config spec.ControllerConfig
	jobs   map[string]*trainer.TrainingJob
	// Kubernetes resource version of the jobs
	jobRVs    map[string]string
	stopChMap map[string]chan struct{}

	// TODO(jlewi): waitJobs should probably be used to ensure TrainingJob has finished processing
	// a stop event before shutting down and deleting all jobs.
	waitJobs sync.WaitGroup
}

func New(kubeCli kubernetes.Interface, apiCli apiextensionsclient.Interface, mxJobClient k8sutil.MxJobClient, ns string, config spec.ControllerConfig) *Controller {
	if mxJobClient == nil {
		panic("mxJobClient can't be nil")
	}
	return &Controller{
		Namespace:   ns,
		KubeCli:     kubeCli,
		ApiCli:      apiCli,
		MxJobClient: mxJobClient,
		// TODO(jlewi)): What to do about cluster.Cluster?
		jobs:      make(map[string]*trainer.TrainingJob),
		jobRVs:    make(map[string]string),
		stopChMap: map[string]chan struct{}{},
		config:    config,
	}
}

func (c *Controller) Run() error {
	var (
		watchVersion string
		err          error
	)

	for {
		watchVersion, err = c.initResource()
		if err == nil {
			break
		}
		log.Errorf("initialization failed: %v", err)
		log.Infof("retry in %v...", initRetryWaitTime)
		time.Sleep(initRetryWaitTime)
		// todo: add max retry?
	}

	log.Infof("starts running from watch version: %s", watchVersion)

	defer func() {
		for _, stopC := range c.stopChMap {
			close(stopC)
		}
		c.waitJobs.Wait()
	}()

	eventCh, errCh := c.watch(watchVersion)

	go func() {
		pt := newPanicTimer(time.Minute, "unexpected long blocking (> 1 Minute) when handling MxJob event")

		for ev := range eventCh {
			pt.start()
			if err := c.handleMxJobEvent(ev); err != nil {
				log.Warningf("fail to handle event: %v, error %v", util.Pformat(ev), err)
			}
			pt.stop()
		}
	}()
	return <-errCh
}

func (c *Controller) handleMxJobEvent(event *Event) error {
	mxjob := event.Object

	if mxjob.Status.IsFailed() {
		if event.Type == kwatch.Deleted {
			delete(c.jobs, mxjob.Key())
			delete(c.jobRVs, mxjob.Key())
			return nil
		}
		return fmt.Errorf("ignore failed MxJob (%s). Please delete its CRD", mxjob.Metadata.Name)
	}

	// TODO: add validation to spec update.
	mxjob.Spec.Cleanup()
	//
	switch event.Type {
	case kwatch.Added:
		// Event indicates that a new instance of the Cluster CRD was created.
		// So we create a Cluster object to control this resource.
		stopC := make(chan struct{})
		trainingJob, err := trainer.NewJob(c.KubeCli, c.MxJobClient, mxjob, stopC, &c.waitJobs, &c.config)
		if err != nil {
			return err
		}

		c.stopChMap[mxjob.Key()] = stopC
		c.jobs[mxjob.Key()] = trainingJob
		c.jobRVs[mxjob.Key()] = mxjob.Metadata.ResourceVersion

	//case kwatch.Modified:
	//  if _, ok := c.jobs[mxjob.Metadata.Namespace + "-" + mxjob.Metadata.Name]; !ok {
	//    return fmt.Errorf("unsafe state. Cluster was never created but we received event (%s)", event.Type)
	//  }
	//  c.jobs[mxjob.Metadata.Namespace + "-" + mxjob.Metadata.Name].Update(mxjob)
	//  c.jobRVs[mxjob.Metadata.Name] = mxjob.Metadata.ResourceVersion
	//
	case kwatch.Deleted:
		if _, ok := c.jobs[mxjob.Key()]; !ok {
			return fmt.Errorf("unsafe state. MxJob was never created but we received event (%s)", event.Type)
		}
		c.jobs[mxjob.Key()].Delete()
		delete(c.jobs, mxjob.Key())
		delete(c.jobRVs, mxjob.Key())
	}
	return nil
}

func (c *Controller) findAllMxJobs() (string, error) {
	// TODO(jlewi): Need to implement this function.
	// TODO: Need to find for all namespaces
	log.Info("finding existing jobs...")
	jobList, err := c.MxJobClient.List(c.Namespace)
	if err != nil {
		return "", err
	}

	for i := range jobList.Items {
		mxjob := jobList.Items[i]

		if mxjob.Status.IsFailed() {
			log.Infof("ignore failed MxJob (%s). Please delete its CRD", mxjob.Metadata.Name)
			continue
		}

		mxjob.Spec.Cleanup()

		stopC := make(chan struct{})
		nc, err := trainer.NewJob(c.KubeCli, c.MxJobClient, &mxjob, stopC, &c.waitJobs, &c.config)

		if err != nil {
			log.Errorf("traininer.NewJob() returned error; %v for job: %v", err, mxjob.Metadata.Name)
			continue
		}
		c.stopChMap[mxjob.Key()] = stopC
		c.jobs[mxjob.Key()] = nc
		c.jobRVs[mxjob.Key()] = mxjob.Metadata.ResourceVersion
	}

	return jobList.Metadata.ResourceVersion, nil
}

// makeClusterConfig creates the Config object from a cluster initializing it with data from the
// controller.
//func (c *Controller) makeClusterConfig() cluster.Config {
//  return cluster.Config{
//    // TODO(jlewi): Do we need a service account?
//    //ServiceAccount: c.Config.ServiceAccount,
//    KubeCli: c.KubeCli,
//  }
//}

func (c *Controller) initResource() (string, error) {
	watchVersion := "0"
	err := c.createCRD()
	if err != nil {
		if k8sutil.IsKubernetesResourceAlreadyExistError(err) {
			// CRD has been initialized before. We need to recover existing cluster.
			watchVersion, err = c.findAllMxJobs()
			if err != nil {
				log.Errorf("initResource() failed; findAllMxJobs returned error: %v", err)
				return "", err
			}
		} else {
			log.Errorf("createCRD() returned error: %v", err)
			return "", fmt.Errorf("fail to create CRD: %v", err)
		}
	}
	return watchVersion, nil
}

func (c *Controller) createCRD() error {
	crd := &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: spec.CRDName(),
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   spec.CRDGroup,
			Version: spec.CRDVersion,
			Scope:   v1beta1.NamespaceScoped,
			Names: v1beta1.CustomResourceDefinitionNames{
				Plural: spec.CRDKindPlural,
				// TODO(jlewi): Do we want to set the singular name?
				// Kind is the serialized kind of the resource.  It is normally CamelCase and singular.
				Kind: reflect.TypeOf(spec.MxJob{}).Name(),
			},
		},
	}

	_, err := c.ApiCli.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	// wait for CRD being established
	err = wait.Poll(500*time.Millisecond, 60*time.Second, func() (bool, error) {
		crd, err = c.ApiCli.ApiextensionsV1beta1().CustomResourceDefinitions().Get(spec.CRDName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			switch cond.Type {
			case v1beta1.Established:
				if cond.Status == v1beta1.ConditionTrue {
					return true, err
				}
			case v1beta1.NamesAccepted:
				if cond.Status == v1beta1.ConditionFalse {
					log.Errorf("Name conflict: %v\n", cond.Reason)
				}
			}
		}
		return false, err
	})

	if err != nil {
		deleteErr := c.ApiCli.ApiextensionsV1beta1().CustomResourceDefinitions().Delete(spec.CRDName(), nil)
		if deleteErr != nil {
			return k8sErrors.NewAggregate([]error{err, deleteErr})
		}
		return err
	}
	return nil
}

// watch creates a go routine, and watches the MX cluster kind resources from
// the given watch version. It emits events on the resources through the returned
// event chan. Errors will be reported through the returned error chan. The go routine
// exits on any error.
func (c *Controller) watch(watchVersion string) (<-chan *Event, <-chan error) {
	eventCh := make(chan *Event)
	// On unexpected error case, controller should exit
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		for {
			resp, err := c.MxJobClient.Watch(MasterHost, c.Namespace, KubeHttpCli, watchVersion)
			if err != nil {
				errCh <- err
				return
			}
			if resp.StatusCode != http.StatusOK {
				log.Infof("WatchClusters response: %+v", resp)
				resp.Body.Close()
				errCh <- errors.New("invalid status code: " + resp.Status)
				return
			}

			log.Infof("start watching at %v", watchVersion)

			decoder := json.NewDecoder(resp.Body)
			for {
				ev, st, err := pollEvent(decoder)
				if err != nil {
					if err == io.EOF { // apiserver will close stream periodically
						log.Info("apiserver closed stream")
						break
					}

					log.Errorf("received invalid event from API server: %v", err)
					errCh <- err
					return
				}

				if st != nil {
					resp.Body.Close()

					if st.Code == http.StatusGone {
						// event history is outdated.
						// if nothing has changed, we can go back to watch again.
						jobList, err := c.MxJobClient.List(c.Namespace)
						if err == nil && !c.isClustersCacheStale(jobList.Items) {
							watchVersion = jobList.Metadata.ResourceVersion
							break
						}

						// if anything has changed (or error on relist), we have to rebuild the state.
						// go to recovery path
						errCh <- ErrVersionOutdated
						return
					}

					log.Fatalf("unexpected status response from API server: %v", st.Message)
				}

				log.Infof("event: %v %v", ev.Type, util.Pformat((ev.Object.Spec)))
				log.Infof("MxJob event: %v %v", ev.Type, util.Pformat(ev.Object.Spec))

				watchVersion = ev.Object.Metadata.ResourceVersion
				eventCh <- ev
			}

			resp.Body.Close()
		}
	}()

	return eventCh, errCh
}

func (c *Controller) isClustersCacheStale(currentClusters []spec.MxJob) bool {
	if len(c.jobRVs) != len(currentClusters) {
		return true
	}

	for _, cc := range currentClusters {
		rv, ok := c.jobRVs[cc.Metadata.Name]
		if !ok || rv != cc.Metadata.ResourceVersion {
			return true
		}
	}

	return false
}
