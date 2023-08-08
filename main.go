// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Authors of KubeArmor

// Package main is responsible for the execution of CLI
package main

import (
	"fmt"

	"github.com/kubearmor/kubearmor-client/k8s"
	"github.com/kubearmor/kubearmor-client/recommend"
	"github.com/rs/zerolog/log"
)

func main() {
	//cmd.Execute()
	client, err := k8s.ConnectK8sClient()
	// fmt.Printf("%v", client.K8sClientset)
	if err != nil {
		log.Error().Msgf("unable to create Kubernetes clients: %s", err.Error())
		return
	}
	err = recommend.Recommendv2(client, recommend.Options{
		Namespace: "",
	})
	if err != nil {
		fmt.Println(err)
	}
}
