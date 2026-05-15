/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package e2e

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/sandstorm/NatsAuthOperator/test/utils"
)

// User Lifecycle is the only e2e spec that exercises the full
// production-shaped pipeline:
//
//	NatsUser CR  →  operator renders ConfigMap  →  kubelet syncs volume
//	             →  operator confirms pod has new content  →  SIGHUP
//	             →  nats-server reloads  →  nats.go client can connect
//	             →  delete CR  →  same chain in reverse  →  client cannot connect
//
// Each historical regression fails this spec for an obvious reason:
//   - $G as a named account → nats-server rejects config → readiness probe
//     never goes green → BeforeAll times out.
//   - missing hash header / parser drift / missing tooling in container →
//     LastConfigHash never advances → the "alice can connect" Eventually
//     waiting on the hash times out.
//   - kubelet sync race → the second It catches a successful connect after
//     deletion (alice was supposed to be gone) → spec fails.
var _ = Describe("User Lifecycle", Ordered, func() {
	const (
		ns          = operatorNamespace
		clusterName = "lifecycle-cluster"
		userName    = "lifecycle-alice"
		secretName  = "lifecycle-alice-nats-nkey"
	)

	// pfStop cancels the kubectl port-forward goroutine in AfterAll.
	var (
		pfCancel context.CancelFunc
		natsAddr string
	)

	BeforeAll(func() {
		// Order matters: bootstrap first so the operator renders the auth
		// ConfigMap before the NATS pod tries to mount it. With the
		// optional-configmap volume the pod *would* still start
		// eventually, but nats-server's `include /etc/nats-config/auth/
		// auth.conf` would fail until the file appears, producing a
		// CrashLoopBackOff window we don't need to sit through.
		By("creating NatsCluster + NatsAccount so the operator renders the auth ConfigMap")
		applyManifest(bootstrapManifests)

		By("waiting for the operator to render the auth ConfigMap")
		Eventually(func(g Gomega) {
			_, err := utils.Run(exec.Command("kubectl", "-n", ns, "get", "configmap", "lifecycle-cluster-nats-config"))
			g.Expect(err).NotTo(HaveOccurred())
		}, 1*time.Minute, time.Second).Should(Succeed())

		By("deploying the NATS server StatefulSet (the operator's reload target)")
		applyManifest(natsServerManifests)

		By("waiting for the NATS pod to become Ready (proves nats-server accepted the rendered config)")
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "-n", ns, "get", "pod",
				"lifecycle-nats-0", "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("True"))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("starting kubectl port-forward to the NATS service")
		pfCtx, cancel := context.WithCancel(context.Background())
		pfCancel = cancel
		var err error
		natsAddr, err = startPortForward(pfCtx, ns, "svc/lifecycle-nats", 4222)
		Expect(err).NotTo(HaveOccurred(), "failed to start port-forward")
		_, _ = fmt.Fprintf(GinkgoWriter, "NATS reachable at %s\n", natsAddr)
	})

	AfterAll(func() {
		if pfCancel != nil {
			pfCancel()
		}

		By("tearing down lifecycle resources")
		// Best-effort, ignore errors so a partial setup still cleans up.
		_, _ = utils.Run(exec.Command("kubectl", "-n", ns, "delete", "natsuser", userName, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "-n", ns, "delete", "natscluster", clusterName, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "-n", ns, "delete", "natsaccount", "lifecycle-acct", "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "-n", ns, "delete", "statefulset", "lifecycle-nats", "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "-n", ns, "delete", "service", "lifecycle-nats", "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "-n", ns, "delete", "configmap",
			"lifecycle-nats-base-config", "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "-n", ns, "delete", "configmap",
			"lifecycle-cluster-nats-config", "--ignore-not-found"))
	})

	It("a NatsUser CR results in a working NATS login", func() {
		By("capturing the pre-alice LastConfigHash so we can detect the operator's next applied change")
		// The cluster+account were already reconciled in BeforeAll, so
		// LastConfigHash is non-empty before alice is even created.
		// Waiting only for "non-empty" returns instantly and the connect
		// races the pod reload — manifesting as Authorization Violation.
		var prevHash string
		Eventually(func(g Gomega) {
			prevHash = getClusterHash(g)
			g.Expect(prevHash).NotTo(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		By("creating the alice NatsUser")
		applyManifest(aliceUserManifest)

		By("waiting for the NKey secret to be generated by the operator")
		Eventually(func(g Gomega) {
			_, err := utils.Run(exec.Command("kubectl", "-n", ns, "get", "secret", secretName))
			g.Expect(err).NotTo(HaveOccurred())
		}, 1*time.Minute, time.Second).Should(Succeed())

		By("waiting for the operator to confirm the running NATS pod has the new auth config" +
			" (LastConfigHash advances past the pre-alice value)")
		Eventually(func(g Gomega) {
			hash := getClusterHash(g)
			g.Expect(hash).NotTo(BeEmpty(), "LastConfigHash must be set once all pods are in sync")
			g.Expect(hash).NotTo(Equal(prevHash),
				"LastConfigHash must advance once the alice-included config is live in the pod")
		}, 3*time.Minute, 2*time.Second).Should(Succeed())

		By("connecting to NATS with alice's credentials")
		seed := readSeed(ns, secretName)
		nc, err := nats.Connect(natsAddr, nkeyOpt(seed))
		Expect(err).NotTo(HaveOccurred(), "fresh credentials must be accepted by NATS")
		defer nc.Close()

		By("round-tripping a message to prove the user is actually authorized (not just authenticated)")
		sub, err := nc.SubscribeSync("events.test")
		Expect(err).NotTo(HaveOccurred())
		Expect(nc.Flush()).To(Succeed())
		Expect(nc.Publish("events.test", []byte("hello"))).To(Succeed())
		msg, err := sub.NextMsg(5 * time.Second)
		Expect(err).NotTo(HaveOccurred(), "publish or subscribe was rejected — permission rendering is broken")
		Expect(string(msg.Data)).To(Equal("hello"))
	})

	It("after deletion, the same credentials no longer authenticate", func() {
		By("capturing the LastConfigHash so we can detect the operator's next applied change")
		var prevHash string
		Eventually(func(g Gomega) {
			prevHash = getClusterHash(g)
			g.Expect(prevHash).NotTo(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		By("re-reading alice's seed before deleting the secret-owning user")
		seed := readSeed(ns, secretName)

		By("deleting the alice NatsUser")
		_, err := utils.Run(exec.Command("kubectl", "-n", ns, "delete", "natsuser", userName))
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the operator to render+apply the user-removed config to the running NATS pod")
		Eventually(func(g Gomega) {
			hash := getClusterHash(g)
			g.Expect(hash).NotTo(BeEmpty())
			g.Expect(hash).NotTo(Equal(prevHash),
				"LastConfigHash must advance once the user-removed config is live in the pod")
		}, 3*time.Minute, 2*time.Second).Should(Succeed())

		By("attempting to authenticate with the now-revoked seed")
		// Use a tight timeout so a hanging connect doesn't stall the suite.
		opts := []nats.Option{
			nkeyOpt(seed),
			nats.Timeout(5 * time.Second),
			nats.MaxReconnects(0),
			nats.NoReconnect(),
		}
		nc, err := nats.Connect(natsAddr, opts...)
		if err == nil {
			defer nc.Close()
		}
		Expect(err).To(HaveOccurred(),
			"NATS accepted a revoked nkey — the auth config did NOT actually get reloaded into the running server")
	})
})

// applyManifest pipes the YAML into `kubectl apply -f -` using the same
// pattern as e2e_test.go.
func applyManifest(yaml string) {
	GinkgoHelper()
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "kubectl apply failed: %s", out)
}

// readSeed pulls the nkey-seed (Ed25519 user seed) from the operator-created
// Secret. Secret stores it as raw bytes under data.nkey-seed which kubectl
// returns base64-encoded.
func readSeed(ns, secret string) []byte {
	GinkgoHelper()
	out, err := utils.Run(exec.Command("kubectl", "-n", ns, "get", "secret", secret,
		"-o", "jsonpath={.data.nkey-seed}"))
	Expect(err).NotTo(HaveOccurred())
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	Expect(err).NotTo(HaveOccurred(), "secret nkey-seed is not valid base64")
	return seed
}

// nkeyOpt builds the nats.Option that signs the server nonce using the user
// seed. nats.Nkey wants the public key plus a callback; we derive the public
// key from the seed so the test only needs to keep the seed around.
func nkeyOpt(seed []byte) nats.Option {
	GinkgoHelper()
	kp, err := nkeys.FromSeed(seed)
	Expect(err).NotTo(HaveOccurred(), "seed bytes are not a valid nkey seed")
	pub, err := kp.PublicKey()
	Expect(err).NotTo(HaveOccurred())
	// Don't Wipe() kp here — the callback below needs it for every nonce.
	return nats.Nkey(pub, func(nonce []byte) ([]byte, error) {
		return kp.Sign(nonce)
	})
}

// getClusterHash reads .status.lastConfigHash off the NatsCluster. Empty
// string means the operator has not yet reported allPodsInSync — i.e. the
// running NATS server is not (yet) confirmed to be on the latest config.
func getClusterHash(g Gomega) string {
	out, err := utils.Run(exec.Command("kubectl", "-n", operatorNamespace, "get", "natscluster", "lifecycle-cluster",
		"-o", "jsonpath={.status.lastConfigHash}"))
	g.Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(out)
}

// startPortForward runs `kubectl port-forward <target> :<remotePort>` in the
// background, parses the local port kubectl chose, and returns the
// localhost:port address. The forwarder runs until ctx is cancelled.
//
// kubectl prints `Forwarding from 127.0.0.1:NNNNN -> 4222` on stdout once
// the listener is ready; we scan for that to know we may safely connect.
func startPortForward(ctx context.Context, ns, target string, remotePort int) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "-n", ns, "port-forward",
		target, fmt.Sprintf(":%d", remotePort))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", err
	}

	addrCh := make(chan string, 1)
	errCh := make(chan error, 1)
	re := regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+)`)

	var once sync.Once
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if m := re.FindStringSubmatch(line); m != nil {
				once.Do(func() { addrCh <- net.JoinHostPort("127.0.0.1", m[1]) })
			}
		}
		// Surface scanner errors only if we haven't already reported the address.
		once.Do(func() { errCh <- fmt.Errorf("kubectl port-forward exited before reporting a local port") })
	}()

	select {
	case addr := <-addrCh:
		return addr, nil
	case err := <-errCh:
		return "", err
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("timed out waiting for kubectl port-forward to be ready")
	}
}
