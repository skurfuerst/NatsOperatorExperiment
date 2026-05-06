package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	natsv1alpha1 "github.com/sandstorm/NatsAuthOperator/api/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
	_ = natsv1alpha1.AddToScheme(scheme)
}

type loadedResources struct {
	Clusters []natsv1alpha1.NatsCluster
	Accounts []natsv1alpha1.NatsAccount
	Users    []natsv1alpha1.NatsUser
}

func loadFromPaths(paths []string) (*loadedResources, error) {
	res := &loadedResources{}
	codecs := serializer.NewCodecFactory(scheme)
	decoder := codecs.UniversalDeserializer()

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		if info.IsDir() {
			files, err := filepath.Glob(filepath.Join(p, "*.yaml"))
			if err != nil {
				return nil, fmt.Errorf("glob %s: %w", p, err)
			}
			ymlFiles, err := filepath.Glob(filepath.Join(p, "*.yml"))
			if err != nil {
				return nil, fmt.Errorf("glob %s: %w", p, err)
			}
			files = append(files, ymlFiles...)
			for _, f := range files {
				if err := loadFile(f, decoder, res); err != nil {
					return nil, err
				}
			}
		} else {
			if err := loadFile(p, decoder, res); err != nil {
				return nil, err
			}
		}
	}

	return res, nil
}

func loadFile(path string, decoder runtime.Decoder, res *loadedResources) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// Split multi-document YAML
	docs := splitYAMLDocuments(data)
	for _, doc := range docs {
		if len(doc) == 0 {
			continue
		}

		obj, gvk, err := decoder.Decode(doc, nil, nil)
		if err != nil {
			// Skip non-NATS resources silently
			continue
		}

		switch gvk.Kind {
		case "NatsCluster":
			if cluster, ok := obj.(*natsv1alpha1.NatsCluster); ok {
				res.Clusters = append(res.Clusters, *cluster)
			}
		case "NatsAccount":
			if account, ok := obj.(*natsv1alpha1.NatsAccount); ok {
				res.Accounts = append(res.Accounts, *account)
			}
		case "NatsUser":
			if user, ok := obj.(*natsv1alpha1.NatsUser); ok {
				res.Users = append(res.Users, *user)
			}
		}
	}

	return nil
}

func splitYAMLDocuments(data []byte) [][]byte {
	var docs [][]byte
	current := make([]byte, 0, len(data))

	for _, line := range splitLines(data) {
		if string(line) == "---" {
			if len(current) > 0 {
				docs = append(docs, current)
				current = nil
			}
			continue
		}
		current = append(current, line...)
		current = append(current, '\n')
	}
	if len(current) > 0 {
		docs = append(docs, current)
	}
	return docs
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
