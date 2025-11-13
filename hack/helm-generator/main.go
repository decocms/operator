package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type K8sResource struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
}

func main() {
	// Get project root from environment or calculate it
	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		wd, _ := os.Getwd()
		projectRoot = wd
	}
	templatesDir := filepath.Join(projectRoot, "chart/templates")

	fmt.Println("Generating Helm chart from Kustomize manifests...")

	// Clean old templates
	fmt.Println("Cleaning old templates...")
	cleanTemplates(templatesDir)

	// Build Kustomize manifests
	fmt.Println("Building Kustomize manifests...")
	kustomizeOutput, err := buildKustomize(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building kustomize: %v\n", err)
		os.Exit(1)
	}

	// Split and process documents
	fmt.Println("Processing manifests...")
	documents := splitYAMLDocuments(kustomizeOutput)
	fileCount := 0

	for _, doc := range documents {
		if len(strings.TrimSpace(doc)) < 20 {
			continue
		}

		// Parse to get kind and name
		var resource K8sResource
		if err := yaml.Unmarshal([]byte(doc), &resource); err != nil {
			continue
		}

		if resource.Kind == "" || resource.Metadata.Name == "" {
			continue
		}

		// Create filename
		kind := strings.ToLower(resource.Kind)
		name := resource.Metadata.Name
		filename := fmt.Sprintf("%s-%s.yaml", kind, name)
		filepath := filepath.Join(templatesDir, filename)

		// Apply template substitutions
		content := applySubstitutions(doc)

		// Add conditionals
		content = addConditionals(content, kind)

		// Write file
		if err := os.WriteFile(filepath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", filename, err)
			continue
		}

		fileCount++
	}

	// Add GITHUB_TOKEN to deployment
	if err := addGitHubTokenToDeployment(templatesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not add GITHUB_TOKEN: %v\n", err)
	}

	fmt.Printf("✓ Generated %d Helm templates\n\n", fileCount)
	fmt.Println("Test with:")
	fmt.Println("  make helm-lint")
	fmt.Println("  make helm-template")
	fmt.Println("")
}

func cleanTemplates(dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	for _, file := range files {
		if !strings.HasSuffix(file, "_helpers.tpl") {
			os.Remove(file)
		}
	}
}

func buildKustomize(projectRoot string) (string, error) {
	cmd := exec.Command(filepath.Join(projectRoot, "bin/kustomize"), "build", "config/default")
	cmd.Dir = projectRoot
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func splitYAMLDocuments(input string) []string {
	return strings.Split(input, "\n---\n")
}

func applySubstitutions(content string) string {
	replacements := map[string]string{
		`image: controller:latest`:                        `image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"`,
		`image: ghcr.io/decocms/operator:latest`:          `image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"`,
		`namespace: operator-system`:                      `namespace: {{ .Release.Namespace }}`,
		`namespace: system`:                               `namespace: {{ .Release.Namespace }}`,
		`name: operator-controller-manager`:               `name: {{ include "operator.fullname" . }}-controller-manager`,
		`serviceAccountName: operator-controller-manager`: `serviceAccountName: {{ include "operator.serviceAccountName" . }}`,
	}

	for old, new := range replacements {
		content = strings.ReplaceAll(content, old, new)
	}

	// Replace operator- prefix in names (but not in namespaces)
	re := regexp.MustCompile(`(?m)^  name: operator-(\w+)`)
	content = re.ReplaceAllString(content, `  name: {{ include "operator.fullname" . }}-$1`)

	return content
}

func addConditionals(content, kind string) string {
	if strings.Contains(kind, "certificate") || strings.Contains(kind, "issuer") {
		return "{{- if .Values.certManager.enabled }}\n" + content + "\n{{- end }}"
	}

	if strings.Contains(kind, "webhook") {
		return "{{- if .Values.webhook.enabled }}\n" + content + "\n{{- end }}"
	}

	return content
}

func addGitHubTokenToDeployment(templatesDir string) error {
	files, err := filepath.Glob(filepath.Join(templatesDir, "deployment-*.yaml"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no deployment file found")
	}

	deploymentFile := files[0]
	content, err := os.ReadFile(deploymentFile)
	if err != nil {
		return err
	}

	contentStr := string(content)

	// Find the image line and add env vars after it
	envBlock := `        {{- if .Values.github.token }}
        env:
        - name: GITHUB_TOKEN
          value: {{ .Values.github.token | quote }}
        {{- end }}`

	re := regexp.MustCompile(`(?m)(        image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}")`)
	contentStr = re.ReplaceAllString(contentStr, "$1\n"+envBlock)

	return os.WriteFile(deploymentFile, []byte(contentStr), 0644)
}
