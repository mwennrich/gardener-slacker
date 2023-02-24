package gardenerslacker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gardener/gardener/pkg/apis/core/v1beta1"
	clientset "github.com/gardener/gardener/pkg/client/core/clientset/versioned"
	gardencoreinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"

	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/klog/v2"
)

type options struct {
	slackURL       string
	kubeconfigPath string
	filename       string
}

type cluster struct {
	Name         string `json:"name"`
	Minimum      int32  `json:"minimum"`
	Maximum      int32  `json:"maximum"`
	ImageName    string `json:"imagename"`
	ImageVersion string `json:"imageversion"`
	APIVersion   string `json:"apiversion"`
	State        string `json:"state"`
}

type slackRequestBody struct {
	Text string `json:"text"`
}

func (o *options) validate() bool {
	// Validate only if the kubeconfig file exits, when a path is given.
	if o.kubeconfigPath != "" {
		if _, err := os.Stat(o.kubeconfigPath); os.IsNotExist(err) {
			klog.Errorf("kubeconfig does not exits on path %s", o.kubeconfigPath)
			return false
		}
	}

	return true
}

// NewStartGardenerSlacker creates a new GardenerSlacker command.
func NewStartGardenerSlacker(ctx context.Context) *cobra.Command {
	options := options{}
	cmd := &cobra.Command{
		Use:  "gardener-slacker",
		Long: "Logs shoot events to slack.",
		Run: func(cmd *cobra.Command, args []string) {
			if !options.validate() {
				os.Exit(1)
			}
			if err := run(ctx, &options); err != nil {
				klog.Error(err.Error())
				os.Exit(1)
			}
		},
	}
	cmd.Flags().StringVar(&options.kubeconfigPath, "kubeconfig", "", "path to kubeconfig file for a Garden cluster")
	cmd.Flags().StringVar(&options.slackURL, "slackurl", "", "URL to slack webhook")
	cmd.Flags().StringVar(&options.filename, "filename", "", "path to db file")

	return cmd

}

func run(ctx context.Context, o *options) error {
	stopCh := make(chan struct{})

	// Create informer factories to create informers.
	gardenInformerFactory, err := setupInformerFactories(o.kubeconfigPath)
	if err != nil {
		return err
	}

	// Start the factories and wait until the creates informes has synce
	var (
		shootInformer = gardenInformerFactory.Core().V1beta1().Shoots().Informer()
	)

	gardenInformerFactory.Start(stopCh)
	if !cache.WaitForCacheSync(ctx.Done(), shootInformer.HasSynced) {

		return errors.New("timed out waiting for Garden caches to sync")
	}

	for {
		is, err := gardenInformerFactory.Core().V1beta1().Shoots().Lister().Shoots(metav1.NamespaceAll).List(labels.Everything())
		if err != nil {
			return err
		}
		newclusters := make(map[string]cluster)
		clusters := readDBJSON(o.filename)
		for _, shoot := range is {
			var newcluster cluster
			newcluster.Name = shoot.Namespace + "/" + shoot.Name
			newcluster.Minimum = shoot.Spec.Provider.Workers[0].Minimum
			newcluster.Maximum = shoot.Spec.Provider.Workers[0].Maximum
			newcluster.ImageName = shoot.Spec.Provider.Workers[0].Machine.Image.Name
			newcluster.ImageVersion = *shoot.Spec.Provider.Workers[0].Machine.Image.Version
			newcluster.APIVersion = shoot.Spec.Kubernetes.Version
			newcluster.State = string(shoot.Status.LastOperation.State)
			newclusters[newcluster.Name] = newcluster

			s, ok := clusters[newcluster.Name]
			if !ok {
				// new shoot found
				sendSlackNotification(ctx, o.slackURL, fmt.Sprintf("new cluster: %s in seed %s", newcluster.Name, *shoot.Spec.SeedName))
				continue
			}
			if s.Minimum != newcluster.Minimum || s.Maximum != newcluster.Maximum {
				// sent to slack
				sendSlackNotification(ctx, o.slackURL, fmt.Sprintf("new cluster sizes for %s: min %d, max %d (old: %d, %d)", newcluster.Name, newcluster.Minimum, newcluster.Maximum, s.Minimum, s.Maximum))
			}
			if s.ImageName != newcluster.ImageName || s.ImageVersion != newcluster.ImageVersion {
				sendSlackNotification(ctx, o.slackURL, fmt.Sprintf("new worker image versions for %s: %s-%s (old: %s-%s)", newcluster.Name, newcluster.ImageName, newcluster.ImageVersion, s.ImageName, s.ImageVersion))
			}
			if s.APIVersion != newcluster.APIVersion {
				sendSlackNotification(ctx, o.slackURL, fmt.Sprintf("new cluster API version for %s: %s (old: %s)", newcluster.Name, newcluster.APIVersion, s.APIVersion))
			}
			if shoot.Status.LastOperation != nil && s.State != newcluster.State && shoot.Status.LastOperation.State == v1beta1.LastOperationStateError {
				msg := fmt.Sprintf("shoot %s has errors: %v\n", newcluster.Name, shoot.Status.LastOperation.Description)
				for _, condition := range shoot.Status.Conditions {
					if condition.Status != v1beta1.ConditionTrue && condition.Status != v1beta1.ConditionProgressing {
						msg = msg + fmt.Sprintf("%s - %v\n", condition.Type, condition.Message)
					}
				}
				sendSlackNotification(o.slackURL, msg)
			}
		}
		for c := range clusters {
			if _, ok := newclusters[c]; !ok {
				sendSlackNotification(ctx, o.slackURL, fmt.Sprintf("cluster %s has been deleted", c))
			}
		}

		writeDBJSON(o.filename, newclusters)
		time.Sleep(1 * time.Minute)
	}
}

// newClientConfig returns rest config to create a k8s clients. In case that
// kubeconfigPath is empty it tries to create in cluster configuration.
func newClientConfig(kubeconfigPath string) (*rest.Config, error) {
	// In cluster configuration
	if kubeconfigPath == "" {
		klog.Info("Use in cluster configuration. This might not work.")
		return rest.InClusterConfig()
	}

	// Kubeconfig based configuration
	kubeconfig, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	configObj, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, err
	}
	if configObj == nil {
		return nil, err
	}
	clientConfig := clientcmd.NewDefaultClientConfig(*configObj, &clientcmd.ConfigOverrides{})
	client, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("ClientConfig is nil")
	}
	return client, nil
}

func setupInformerFactories(kubeconfigPath string) (gardencoreinformers.SharedInformerFactory, error) {
	restConfig, err := newClientConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	if restConfig == nil {
		return nil, err
	}
	gardenClient, err := clientset.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	if gardenClient == nil {
		return nil, errors.New("gardenClient is nil")
	}
	gardenInformerFactory := gardencoreinformers.NewSharedInformerFactory(gardenClient, 0)

	return gardenInformerFactory, nil
}

func readDBJSON(filename string) map[string]cluster {

	db := make(map[string]cluster)
	_, err := os.Stat(filename)
	if !os.IsNotExist(err) {
		f, err := os.Open(filename)
		if err != nil {
			klog.Error(err)
		}
		j, _ := io.ReadAll(f)
		f.Close()
		if err != nil {
			klog.Error(err)
		}
		if err = json.Unmarshal(j, &db); err != nil {
			klog.Error(err)
		}
	} else {
		klog.Infof("file %s does not exist", filename)
	}
	return db
}

func writeDBJSON(filename string, clusters map[string]cluster) {
	j, err := json.Marshal(clusters)
	if err != nil {
		klog.Error(err)
	}
	err = os.WriteFile(filename, j, 0600)
	if err != nil {
		klog.Error(err.Error)
	}
}

func sendSlackNotification(ctx context.Context, slackUIRL string, msg string) {
	slackBody, err := json.Marshal(slackRequestBody{Text: msg})
	if err != nil {
		klog.Error(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackUIRL, bytes.NewBuffer(slackBody))
	if err != nil {
		klog.Error(err)
	}

	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		klog.Error(err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		klog.Error(err)
	}
	if buf.String() != "ok" {
		klog.Error(errors.New("non-ok response returned from Slack"))
	}
}
