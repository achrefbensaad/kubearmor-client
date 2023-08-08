package genericpolicies

import (
	_ "embed" // need for embedding
	"fmt"
	"path/filepath"

	"regexp"
	"strings"

	"github.com/fatih/color"
	pol "github.com/kubearmor/KubeArmor/pkg/KubeArmorController/api/security.kubearmor.com/v1"
	"github.com/kubearmor/kubearmor-client/recommend/image"
	"golang.org/x/exp/slices"
)

const (
	org   = "kubearmor"
	repo  = "policy-templates"
	url   = "https://github.com/kubearmor/policy-templates/archive/refs/tags/"
	cache = ".cache/karmor/"
)

type GenericPolicy struct {
}

// MatchSpec spec to match for defining policy
type MatchSpec struct {
	Name         string                  `json:"name" yaml:"name"`
	Precondition []string                `json:"precondition" yaml:"precondition"`
	Description  Description             `json:"description" yaml:"description"`
	Yaml         string                  `json:"yaml" yaml:"yaml"`
	Spec         pol.KubeArmorPolicySpec `json:"spec,omitempty" yaml:"spec,omitempty"`
}

// Ref for the policy rules
type Ref struct {
	Name string   `json:"name" yaml:"name"`
	URL  []string `json:"url" yaml:"url"`
}

// Description detailed description for the policy rule
type Description struct {
	Refs     []Ref  `json:"refs" yaml:"refs"`
	Tldr     string `json:"tldr" yaml:"tldr"`
	Detailed string `json:"detailed" yaml:"detailed"`
}

func (P GenericPolicy) Init() error {
	if _, err := DownloadAndUnzipRelease(); err != nil {
		return err
	}
	return nil
}

func (P GenericPolicy) Scan(img *image.ImageInfo, tags []string) error {
	getPolicyFromImageInfo(img, tags)
	return nil
}

func checkForSpec(spec string, fl []string) []string {
	var matches []string
	if !strings.HasSuffix(spec, "*") {
		spec = fmt.Sprintf("%s$", spec)
	}

	re := regexp.MustCompile(spec)
	for _, name := range fl {
		if re.Match([]byte(name)) {
			matches = append(matches, name)
		}
	}
	return matches
}

func matchTags(ms *MatchSpec, tags []string) bool {
	if len(tags) <= 0 {
		return true
	}
	for _, t := range tags {
		if slices.Contains(ms.Spec.Tags, t) {
			return true
		}
	}
	return false
}

func checkPreconditions(img *image.ImageInfo, ms *MatchSpec) bool {
	var matches []string
	for _, preCondition := range ms.Precondition {
		matches = append(matches, checkForSpec(filepath.Join(preCondition), img.FileList)...)
		if strings.Contains(preCondition, "OPTSCAN") {
			return true
		}
	}
	return len(matches) >= len(ms.Precondition)
}

func getPolicyFromImageInfo(img *image.ImageInfo, tags []string) {
	if img.OS != "linux" {
		color.Red("non-linux platforms are not supported, yet.")
		return
	}

	fmt.Println(tags)
	idx := 0
	// TODO
	/* if err := ReportStart(img); err != nil {
		log.WithError(err).Error("report start failed")
		return
	} */
	var ms MatchSpec
	var err error

	ms, err = getNextRule(&idx)
	for ; err == nil; ms, err = getNextRule(&idx) {

		if !matchTags(&ms, tags) {
			continue
		}

		if !checkPreconditions(img, &ms) {
			continue
		}
		img.writePolicyFile(ms)
	}
}
