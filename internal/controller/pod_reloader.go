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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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

// IsConfigCurrent execs `cat <AuthConfPath>` inside the pod and parses the
// "# hash: <sha256>" header from the first line, returning true iff it
// matches expectedHash. We use `cat` rather than `head -n1` because not every
// minimal NATS container image ships head on PATH; cat is universally
// available wherever a shell-less SPDY exec works at all.
//
// Returns (false, nil) when the file is absent / unreadable / has no header,
// after logging diagnostic detail so unexpected exec failures are visible
// rather than silently looping the reconciler.
func (r *SpdyPodReloader) IsConfigCurrent(ctx context.Context, namespace, podName, expectedHash string) (bool, error) {
	log := logf.FromContext(ctx)

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
			Command: []string{"cat", AuthConfPath},
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
	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if streamErr != nil {
		// Log loudly so we don't silently loop forever. Even on error we
		// fall through to parse stdout — some non-zero exits still produce
		// useful output (and we'd rather try than pretend it's stale).
		log.Info("exec to read auth.conf returned error",
			"pod", podName, "err", streamErr.Error(),
			"stderr", stderr.String(), "stdoutLen", stdout.Len())
	}
	parsed := parseHashHeader(stdout.String())
	if parsed == "" {
		log.Info("could not parse hash header from pod auth.conf",
			"pod", podName, "stdoutPrefix", stdoutPreview(stdout.String()),
			"stderr", stderr.String())
		return false, nil
	}
	return parsed == expectedHash, nil
}

// stdoutPreview returns the first ~120 chars of s, with newlines escaped,
// suitable for one-line log output.
func stdoutPreview(s string) string {
	const max = 120
	if len(s) > max {
		s = s[:max]
	}
	return strings.ReplaceAll(s, "\n", `\n`)
}

// parseHashHeader extracts the sha256 from the first non-empty line of s
// when it has the form "# hash: <sha256>". Tolerates leading blank lines,
// CR characters and surrounding whitespace. Returns "" if the first
// non-empty line is not a recognizable header.
func parseHashHeader(s string) string {
	for _, line := range strings.SplitN(s, "\n", 8) {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			return ""
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if !strings.HasPrefix(rest, "hash:") {
			return ""
		}
		return strings.TrimSpace(strings.TrimPrefix(rest, "hash:"))
	}
	return ""
}

// FormatHashHeader returns the "# hash: <sha256>\n" line that is prepended
// to the rendered auth.conf so pods can be queried for which version of the
// config they currently have mounted.
func FormatHashHeader(hash string) string {
	return fmt.Sprintf("# hash: %s\n", hash)
}
