package genericpolicies

import (
	"archive/zip"
	"context"
	_ "embed" // need for embedding
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cavaliergopher/grab/v3"
	"github.com/fatih/color"
	"github.com/google/go-github/github"
	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	pol "github.com/kubearmor/KubeArmor/pkg/KubeArmorController/api/security.kubearmor.com/v1"
	log "github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"
)

func isLatest() bool {
	LatestVersion := latestRelease()
	CurrentVersion := CurrentRelease()
	if LatestVersion == "" {
		// error while fetching latest release tag
		// assume the current release is the latest one
		return true
	}
	return (CurrentVersion == LatestVersion)
}

func latestRelease() string {
	latestRelease, _, err := github.NewClient(nil).Repositories.GetLatestRelease(context.Background(), org, repo)
	if err != nil {
		log.WithError(err)
		return ""
	}
	return *latestRelease.TagName
}

// CurrentRelease gets the current release of policy-templates
func CurrentRelease() string {
	CurrentVersion := ""
	path, err := os.ReadFile(fmt.Sprintf("%s%s", getCachePath(), "rules.yaml"))
	if err != nil {
		CurrentVersion = strings.Trim(updateRulesYAML([]byte{}), "\"")
	} else {

		CurrentVersion = strings.Trim(updateRulesYAML(path), "\"")
	}
	return CurrentVersion
}

func getCachePath() string {
	cache := fmt.Sprintf("%s/%s", UserHome(), cache)
	return cache

}

// UserHome function returns users home directory
func UserHome() string {
	if runtime.GOOS == "windows" {
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		return home
	}
	return os.Getenv("HOME")
}

//go:embed yaml/rules.yaml

var policyRulesYAML []byte

var policyRules []MatchSpec

func updateRulesYAML(yamlFile []byte) string {
	policyRules = []MatchSpec{}
	if len(yamlFile) < 30 {
		yamlFile = policyRulesYAML
	}
	policyRulesJSON, err := yaml.YAMLToJSON(yamlFile)
	if err != nil {
		color.Red("failed to convert policy rules yaml to json")
		log.WithError(err).Fatal("failed to convert policy rules yaml to json")
	}
	var jsonRaw map[string]json.RawMessage
	err = json.Unmarshal(policyRulesJSON, &jsonRaw)
	if err != nil {
		color.Red("failed to unmarshal policy rules json")
		log.WithError(err).Fatal("failed to unmarshal policy rules json")
	}
	err = json.Unmarshal(jsonRaw["policyRules"], &policyRules)
	if err != nil {
		color.Red("failed to unmarshal policy rules")
		log.WithError(err).Fatal("failed to unmarshal policy rules")
	}
	return string(jsonRaw["version"])
}

func removeData(file string) error {
	err := os.RemoveAll(file)
	return err
}

// DownloadAndUnzipRelease downloads the latest version of policy-templates
func DownloadAndUnzipRelease() (string, error) {
	latestVersion := latestRelease()
	currentVersion := CurrentRelease()
	return latestVersion, nil

	if isLatest() {
		return latestVersion, nil
	}

	log.WithFields(log.Fields{
		"Current Version": currentVersion,
	}).Info("Found outdated version of policy-templates")
	log.Info("Downloading latest version [", latestVersion, "]")

	_ = removeData(getCachePath())
	err := os.MkdirAll(filepath.Dir(getCachePath()), 0750)
	if err != nil {
		return "", err
	}
	downloadURL := fmt.Sprintf("%s%s.zip", url, latestVersion)
	resp, err := grab.Get(getCachePath(), downloadURL)
	if err != nil {
		_ = removeData(getCachePath())
		return "", err
	}
	err = unZip(resp.Filename, getCachePath())
	if err != nil {
		return "", err
	}
	err = removeData(resp.Filename)
	if err != nil {
		return "", err
	}
	_ = updatePolicyRules(strings.TrimSuffix(resp.Filename, ".zip"))

	log.WithFields(log.Fields{
		"Updated Version": latestVersion,
	}).Info("policy-templates updated")
	return latestVersion, nil
}

// Sanitize archive file pathing from "G305: Zip Slip vulnerability"
func sanitizeArchivePath(d, t string) (v string, err error) {
	v = filepath.Join(d, t)
	if strings.HasPrefix(v, filepath.Clean(d)) {
		return v, nil
	}

	return "", fmt.Errorf("%s: %s", "content filepath is tainted", t)
}

func unZip(source, dest string) error {
	read, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer read.Close()
	for _, file := range read.File {
		if file.Mode().IsDir() {
			continue
		}
		open, err := file.Open()
		if err != nil {
			return err
		}
		name, err := sanitizeArchivePath(dest, file.Name)
		if err != nil {
			return err
		}
		_ = os.MkdirAll(path.Dir(name), 0750)
		create, err := os.Create(filepath.Clean(name))
		if err != nil {
			return err
		}
		_, err = create.ReadFrom(open)
		if err != nil {
			return err
		}
		if err = create.Close(); err != nil {
			return err
		}
		defer func() {
			if err := open.Close(); err != nil {
				kg.Warnf("Error closing io stream %s\n", err)
			}
		}()
	}
	return nil
}

func getNextRule(idx *int) (MatchSpec, error) {
	if *idx < 0 {
		(*idx)++
	}
	if *idx >= len(policyRules) {
		return MatchSpec{}, errors.New("no rule at idx")
	}
	r := policyRules[*idx]
	(*idx)++
	return r, nil
}

func updatePolicyRules(filePath string) error {
	var files []string
	err := filepath.Walk(filePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "metadata.yaml" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	rulesYamlPath := filepath.Join(getCachePath(), "rules.yaml")
	f, err := os.Create(filepath.Clean(rulesYamlPath))
	if err != nil {
		log.WithError(err).Errorf("Failed to create %s", rulesYamlPath)
	}

	var yamlFile []byte
	var completePolicy []MatchSpec
	var version string

	for _, file := range files {
		idx := 0
		yamlFile, err = os.ReadFile(filepath.Clean(file))
		if err != nil {
			return err
		}
		version = updateRulesYAML(yamlFile)
		ms, err := getNextRule(&idx)
		for ; err == nil; ms, err = getNextRule(&idx) {
			if ms.Yaml != "" {
				var policy map[string]interface{}
				newYaml, err := os.ReadFile(filepath.Clean(fmt.Sprintf("%s%s", strings.TrimSuffix(file, "metadata.yaml"), ms.Yaml)))
				if err != nil {
					newYaml, _ = os.ReadFile(filepath.Clean(fmt.Sprintf("%s/%s", filePath, ms.Yaml)))
				}
				err = yaml.Unmarshal(newYaml, &policy)
				if err != nil {
					return err
				}
				apiVersion := policy["apiVersion"].(string)
				if strings.Contains(apiVersion, "kubearmor") {
					var kubeArmorPolicy pol.KubeArmorPolicy
					err = yaml.Unmarshal(newYaml, &kubeArmorPolicy)
					if err != nil {
						return err
					}
					ms.Spec = kubeArmorPolicy.Spec
				} else {
					continue
				}
				ms.Yaml = ""
			}
			completePolicy = append(completePolicy, ms)
		}
	}
	policyRules = completePolicy
	yamlFile, err = yaml.Marshal(completePolicy)
	if err != nil {
		return err
	}
	version = strings.Trim(version, "\"")
	yamlFile = []byte(fmt.Sprintf("version: %s\npolicyRules:\n%s", version, yamlFile))
	if _, err := f.WriteString(string(yamlFile)); err != nil {
		log.WithError(err).Error("WriteString failed")
	}
	if err := f.Sync(); err != nil {
		log.WithError(err).Error("file sync failed")
	}
	if err := f.Close(); err != nil {
		log.WithError(err).Error("file close failed")
	}

	return nil
}
