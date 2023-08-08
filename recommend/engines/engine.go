package engines

import "github.com/kubearmor/kubearmor-client/recommend/image"

type Engine interface {
	Init() error
	Scan(img *image.ImageInfo, tags []string) error
}
