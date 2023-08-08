package recommend

import (
	"context"

	"github.com/kubearmor/kubearmor-client/k8s"
	genericpolicies "github.com/kubearmor/kubearmor-client/recommend/engines/generic_policies"
	"github.com/kubearmor/kubearmor-client/recommend/image"
	"github.com/kubearmor/kubearmor-client/recommend/registry"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Recommend handler for karmor cli tool
func Recommendv2(c *k8s.Client, o Options) error {
	deployments := []Deployment{}
	labelMap := labelArrayToLabelMap(o.Labels)
	if len(o.Images) == 0 {
		// recommendation based on k8s manifest
		dps, err := c.K8sClientset.AppsV1().Deployments(o.Namespace).List(context.TODO(), v1.ListOptions{})
		if err != nil {
			return err
		}
		for _, dp := range dps.Items {

			if matchLabels(labelMap, dp.Spec.Template.Labels) {
				images := []string{}
				for _, container := range dp.Spec.Template.Spec.Containers {
					images = append(images, container.Image)
				}

				deployments = append(deployments, Deployment{
					Name:      dp.Name,
					Namespace: dp.Namespace,
					Labels:    dp.Spec.Template.Labels,
					Images:    images,
				})
			}
		}
	} else {
		deployments = append(deployments, Deployment{
			Namespace: o.Namespace,
			Labels:    labelMap,
			Images:    o.Images,
		})
	}

	o.Tags = unique(o.Tags)
	options = o
	_ = deployments
	reg := registry.New("/home/vagrant/.docker/config")

	gen := genericpolicies.GenericPolicy{}
	gen.Init()

	for _, deployment := range deployments {
		for _, i := range deployment.Images {
			img := image.ImageInfo{
				Name:      i,
				Namespace: deployment.Namespace,
				Labels:    deployment.Labels,
				Image:     i,
			}
			reg.Analyze(&img)

			gen.Scan(&img, []string{})
		}
	}

	return nil
}
