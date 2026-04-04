/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// PodReloader sends a SIGHUP signal to a running pod to trigger a NATS server
// configuration reload without restarting the process.
type PodReloader interface {
	ReloadPod(ctx context.Context, namespace, podName string) error
}

// SpdyPodReloader implements PodReloader using the Kubernetes exec API over SPDY.
// It execs `kill -HUP 1` inside the target pod, which sends SIGHUP to the NATS
// server process (assumed to be PID 1 in the container).
type SpdyPodReloader struct {
	RestConfig *rest.Config
}

func (r *SpdyPodReloader) ReloadPod(ctx context.Context, namespace, podName string) error {
	clientset, err := kubernetes.NewForConfig(r.RestConfig)
	if err != nil {
		return err
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"kill", "-HUP", "1"},
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, clientgoscheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(r.RestConfig, "POST", req.URL())
	if err != nil {
		return err
	}

	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
}
