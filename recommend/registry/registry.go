// SPDX-License-Identifier: Apache-2.0
// Copyright 2022 Authors of KubeArmor

package registry

import (
	"archive/tar"
	"bufio"
	"context"
	_ "embed" // need for embedding
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	image "github.com/kubearmor/kubearmor-client/recommend/image"
	"github.com/moby/term"

	dockerTypes "github.com/docker/docker/api/types"
	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	"github.com/kubearmor/kubearmor-client/hacks"
	log "github.com/sirupsen/logrus"
)

const karmorTempDirPattern = "karmor"

var random *rand.Rand

func init() {
	random = rand.New(rand.NewSource(time.Now().UnixNano())) // random seed init for random string generator
}

type RegistryScanner struct {
	authConfiguration authConfigurations
	cli               *client.Client // docker client
	cache             map[string]image.ImageInfo
}

// authConfigurations contains the configuration information's
type authConfigurations struct {
	configPath string // stores path of docker config.json
	authCreds  []string
}

func getAuthStr(u, p string) string {
	if u == "" || p == "" {
		return ""
	}

	encodedJSON, err := json.Marshal(dockerTypes.AuthConfig{
		Username: u,
		Password: p,
	})
	if err != nil {
		log.WithError(err).Fatal("failed to marshal credentials")
	}

	return base64.URLEncoding.EncodeToString(encodedJSON)
}

func (r *RegistryScanner) loadDockerAuthConfigs() {
	r.authConfiguration.authCreds = append(r.authConfiguration.authCreds, fmt.Sprintf("%s:%s", os.Getenv("DOCKER_USERNAME"), os.Getenv("DOCKER_PASSWORD")))
	if r.authConfiguration.configPath != "" {
		data, err := os.ReadFile(filepath.Clean(r.authConfiguration.configPath))
		if err != nil {
			return
		}

		confsWrapper := struct {
			Auths map[string]dockerTypes.AuthConfig `json:"auths"`
		}{}
		_ = json.Unmarshal(data, &confsWrapper)

		for _, conf := range confsWrapper.Auths {
			data, _ := base64.StdEncoding.DecodeString(conf.Auth)
			userPass := strings.SplitN(string(data), ":", 2)
			r.authConfiguration.authCreds = append(r.authConfiguration.authCreds, getAuthStr(userPass[0], userPass[1]))
		}
		log.Warn(r.authConfiguration.authCreds)
	}
}

func New(dockerConfigPath string) *RegistryScanner {
	var err error
	scanner := RegistryScanner{
		authConfiguration: authConfigurations{
			configPath: dockerConfigPath,
		},
		cache: make(map[string]image.ImageInfo),
	}

	if err != nil {
		log.WithError(err).Error("could not create temp dir")
	}

	scanner.cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.WithError(err).Fatal("could not create new docker client")
	}
	scanner.loadDockerAuthConfigs()

	return &scanner
}

func (r *RegistryScanner) Analyze(img *image.ImageInfo) (err error) {
	if val, ok := r.cache[img.Name]; ok {
		log.WithFields(log.Fields{
			"image": img.Name,
		}).Infof("Image already scanned in this session, using cached informations for image")
		img.Arch = val.Arch
		img.DirList = val.DirList
		img.FileList = val.FileList
		img.Distro = val.Distro
		img.Labels = val.Labels
		img.OS = val.OS
		img.RepoTags = val.RepoTags
		return nil
	}
	tmpDir, err := os.MkdirTemp("", karmorTempDirPattern)
	if err != nil {
		log.WithError(err).Error("could not create temp dir")
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()
	img.TempDir = tmpDir
	err = r.pullImage(img.Name, tmpDir)
	if err != nil {
		log.Warn("Failed to pull image. Dumping generic policies.")
		img.OS = "linux"
		img.RepoTags = append(img.RepoTags, img.Name)
	} else {
		tarname := saveImageToTar(img.Name, r.cli, tmpDir)
		img.FileList, img.DirList = extractTar(tarname, tmpDir)
		img.GetImageInfo()
	}

	r.cache[img.Name] = *img
	return nil
}

// The randomizer used in this function is not used for any cryptographic
// operation and hence safe to use.
func randString(n int) string {
	var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))] // #nosec
	}
	return string(b)
}

func (r *RegistryScanner) pullImage(imageName, tempDir string) (err error) {
	log.WithFields(log.Fields{
		"image": imageName,
	}).Info("pulling image")

	var out io.ReadCloser

	for _, cred := range r.authConfiguration.authCreds {
		out, err = r.cli.ImagePull(context.Background(), imageName,
			dockerTypes.ImagePullOptions{
				RegistryAuth: cred,
			})
		if err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	defer func() {
		if err := out.Close(); err != nil {
			kg.Warnf("Error closing io stream %s\n", err)
		}
	}()
	termFd, isTerm := term.GetFdInfo(os.Stderr)
	err = jsonmessage.DisplayJSONMessagesStream(out, os.Stderr, termFd, isTerm, nil)
	if err != nil {
		log.WithError(err).Error("could not display json")
	}

	return
}

// Sanitize archive file pathing from "G305: Zip Slip vulnerability"
func sanitizeArchivePath(d, t string) (v string, err error) {
	v = filepath.Join(d, t)
	if strings.HasPrefix(v, filepath.Clean(d)) {
		return v, nil
	}

	return "", fmt.Errorf("%s: %s", "content filepath is tainted", t)
}

func extractTar(tarname string, tempDir string) ([]string, []string) {
	var fl []string
	var dl []string

	f, err := os.Open(filepath.Clean(tarname))
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"tar": tarname,
		}).Fatal("os create failed")
	}
	defer hacks.CloseCheckErr(f, tarname)

	tr := tar.NewReader(bufio.NewReader(f))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			log.WithError(err).Fatal("tar next failed")
		}

		tgt, err := sanitizeArchivePath(tempDir, hdr.Name)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"file": hdr.Name,
			}).Error("ignoring file since it could not be sanitized")
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if _, err := os.Stat(tgt); err != nil {
				if err := os.MkdirAll(tgt, 0750); err != nil {
					log.WithError(err).WithFields(log.Fields{
						"target": tgt,
					}).Fatal("tar mkdirall")
				}
			}
			dl = append(dl, tgt)
		case tar.TypeReg:
			f, err := os.OpenFile(filepath.Clean(tgt), os.O_CREATE|os.O_RDWR, os.FileMode(hdr.Mode))
			if err != nil {
				log.WithError(err).WithFields(log.Fields{
					"target": tgt,
				}).Error("tar open file")
			} else {

				// copy over contents
				if _, err := io.CopyN(f, tr, 2e+9 /*2GB*/); err != io.EOF {
					log.WithError(err).WithFields(log.Fields{
						"target": tgt,
					}).Fatal("tar io.Copy()")
				}
			}
			hacks.CloseCheckErr(f, tgt)
			if strings.HasSuffix(tgt, "layer.tar") { // deflate container image layer
				ifl, idl := extractTar(tgt, tempDir)
				fl = append(fl, ifl...)
				dl = append(dl, idl...)
			} else {
				fl = append(fl, tgt)
			}
		}
	}
	return fl, dl
}

func saveImageToTar(imageName string, cli *client.Client, tempDir string) string {
	imgdata, err := cli.ImageSave(context.Background(), []string{imageName})
	if err != nil {
		log.WithError(err).Fatal("could not save image")
	}
	defer func() {
		if err := imgdata.Close(); err != nil {
			kg.Warnf("Error closing io stream %s\n", err)
		}
	}()

	tarname := filepath.Join(tempDir, randString(8)+".tar")

	f, err := os.Create(filepath.Clean(tarname))
	if err != nil {
		log.WithError(err).Fatal("os create failed")
	}

	if _, err := io.CopyN(bufio.NewWriter(f), imgdata, 5e+9 /*5GB*/); err != io.EOF {
		log.WithError(err).WithFields(log.Fields{
			"tar": tarname,
		}).Fatal("io.CopyN() failed")
	}
	hacks.CloseCheckErr(f, tarname)
	log.WithFields(log.Fields{
		"tar": tarname,
	}).Info("dumped image to tar")
	return tarname
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
