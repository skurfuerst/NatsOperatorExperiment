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
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// AuthConfPath is the canonical mount path for the rendered NATS auth.conf
// inside NATS server pods. The header line of this file ("# hash: <sha256>")
// is used by the operator to verify that the kubelet has finished syncing
// the latest ConfigMap into the pod's volume before triggering a SIGHUP.
const AuthConfPath = "/etc/nats-config/auth/auth.conf"

// PodReloader sends a SIGHUP signal to a running pod to trigger a NATS server
// configuration reload, and verifies whether the pod's mounted auth.conf
// already contains the expected content version (so SIGHUP is not sent
// against a kubelet-stale volume).
type PodReloader interface {
	ReloadPod(ctx context.Context, namespace, podName string) error
	// IsConfigCurrent returns true if the auth.conf mounted inside the pod
	// has a "# hash: <sha256>" header that matches expectedHash. Returns
	// false if the file is missing, unreadable, or shows an older hash.
	IsConfigCurrent(ctx context.Context, namespace, podName, expectedHash string) (bool, error)
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

// IsConfigCurrent execs `head -n1 <AuthConfPath>` inside the pod and parses
// the "# hash: <sha256>" header, returning true iff it matches expectedHash.
// File absent / unreadable / no header → returns false (treated as stale).
func (r *SpdyPodReloader) IsConfigCurrent(ctx context.Context, namespace, podName, expectedHash string) (bool, error) {
	clientset, err := kubernetes.NewForConfig(r.RestConfig)
	if err != nil {
		return false, err
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"head", "-n", "1", AuthConfPath},
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, clientgoscheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(r.RestConfig, "POST", req.URL())
	if err != nil {
		return false, err
	}

	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		// File missing / unreadable — treat as "stale", not as a hard error.
		return false, nil
	}
	return parseHashHeader(stdout.String()) == expectedHash, nil
}

// parseHashHeader extracts the sha256 from a "# hash: <sha256>" line.
// Returns "" if the input does not start with a recognizable header.
func parseHashHeader(s string) string {
	line := s
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	const prefix = "# hash:"
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix))
}

// FormatHashHeader returns the "# hash: <sha256>\n" line that is prepended
// to the rendered auth.conf so pods can be queried for which version of the
// config they currently have mounted.
func FormatHashHeader(hash string) string {
	return fmt.Sprintf("# hash: %s\n", hash)
}
