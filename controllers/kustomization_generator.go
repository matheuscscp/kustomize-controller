package controllers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/k8sdeps/kunstruct"
	"sigs.k8s.io/kustomize/api/konfig"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
)

const (
	kustomizationFileName = "kustomization.yaml"
	transformerFileName   = "kustomization-gc-labels.yaml"
)

type KustomizeGenerator struct {
	Kustomization kustomizev1.Kustomization
	Revision      string
}

func (kg KustomizeGenerator) WriteFile(dirPath string) error {
	kfile := filepath.Join(dirPath, kustomizationFileName)

	if err := kg.generateKustomization(dirPath); err != nil {
		return fmt.Errorf("kustomize create failed: %w", err)
	}

	if err := kg.generateLabelTransformer(dirPath); err != nil {
		return err
	}

	data, err := ioutil.ReadFile(kfile)
	if err != nil {
		return err
	}

	kus := kustypes.Kustomization{
		TypeMeta: kustypes.TypeMeta{
			APIVersion: kustypes.KustomizationVersion,
			Kind:       kustypes.KustomizationKind,
		},
	}

	if err := yaml.Unmarshal(data, &kus); err != nil {
		return err
	}

	if len(kus.Transformers) == 0 {
		kus.Transformers = []string{transformerFileName}
	} else {
		var exists bool
		for _, transformer := range kus.Transformers {
			if transformer == transformerFileName {
				exists = true
				break
			}
		}
		if !exists {
			kus.Transformers = append(kus.Transformers, transformerFileName)
		}
	}

	kd, err := yaml.Marshal(kus)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(kfile, kd, os.ModePerm)
}

func (kg KustomizeGenerator) generateKustomization(dirPath string) error {
	fs := filesys.MakeFsOnDisk()
	kfile := filepath.Join(dirPath, kustomizationFileName)

	scan := func(base string) ([]string, error) {
		var paths []string
		uf := kunstruct.NewKunstructuredFactoryImpl()
		err := fs.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if path == base {
				return nil
			}
			if info.IsDir() {
				// If a sub-directory contains an existing kustomization file add the
				// directory as a resource and do not decend into it.
				for _, kfilename := range konfig.RecognizedKustomizationFileNames() {
					if fs.Exists(filepath.Join(path, kfilename)) {
						paths = append(paths, path)
						return filepath.SkipDir
					}
				}
				return nil
			}
			fContents, err := fs.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := uf.SliceFromBytes(fContents); err != nil {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
		return paths, err
	}

	if _, err := os.Stat(kfile); err != nil {
		abs, err := filepath.Abs(dirPath)
		if err != nil {
			return err
		}

		files, err := scan(abs)
		if err != nil {
			return err
		}

		f, err := fs.Create(kfile)
		if err != nil {
			return err
		}
		f.Close()

		kus := kustypes.Kustomization{
			TypeMeta: kustypes.TypeMeta{
				APIVersion: kustypes.KustomizationVersion,
				Kind:       kustypes.KustomizationKind,
			},
		}

		var resources []string
		for _, file := range files {
			resources = append(resources, strings.Replace(file, abs, ".", 1))
		}

		kus.Resources = resources
		kd, err := yaml.Marshal(kus)
		if err != nil {
			return err
		}

		return ioutil.WriteFile(kfile, kd, os.ModePerm)
	}

	return nil
}

func (kg KustomizeGenerator) generateLabelTransformer(dirPath string) error {
	var lt = struct {
		ApiVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
		Metadata   struct {
			Name string `json:"name" yaml:"name"`
		} `json:"metadata" yaml:"metadata"`
		Labels     map[string]string    `json:"labels,omitempty" yaml:"labels,omitempty"`
		FieldSpecs []kustypes.FieldSpec `json:"fieldSpecs,omitempty" yaml:"fieldSpecs,omitempty"`
	}{
		ApiVersion: "builtin",
		Kind:       "LabelTransformer",
		Metadata: struct {
			Name string `json:"name" yaml:"name"`
		}{
			Name: kg.Kustomization.GetName(),
		},
		Labels: gcLabels(kg.Kustomization.GetName(), kg.Kustomization.GetNamespace(), kg.Revision),
		FieldSpecs: []kustypes.FieldSpec{
			{Path: "metadata/labels", CreateIfNotPresent: true},
		},
	}

	data, err := yaml.Marshal(lt)
	if err != nil {
		return err
	}

	labelsFile := filepath.Join(dirPath, transformerFileName)
	if err := ioutil.WriteFile(labelsFile, data, os.ModePerm); err != nil {
		return err
	}

	return nil
}
