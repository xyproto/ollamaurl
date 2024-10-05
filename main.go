package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

const (
	versionString    = "ollamaurl 1.0.2"
	defaultRegistry  = "https://registry.ollama.ai"
	defaultModelTag  = "tinyllama:latest"
	manifestFilename = "manifest.json"
)

type Layer struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
}

type Manifest struct {
	SchemaVersion int     `json:"schemaVersion"`
	MediaType     string  `json:"mediaType"`
	Config        Layer   `json:"config"`
	Layers        []Layer `json:"layers"`
}

type Client struct {
	base *url.URL
	http *http.Client
}

func NewClient(base *url.URL, httpClient *http.Client) *Client {
	return &Client{
		base: base,
		http: httpClient,
	}
}

func ParseModelPath(name string) (string, string) {
	repo, tag, found := strings.Cut(name, ":")
	if !found {
		tag = "latest"
	}
	return repo, tag
}

// GetManifest retrieves the model's manifest from the registry
func (c *Client) GetManifest(ctx context.Context, modelName, tag string, verbose bool) (*Manifest, error) {
	manifestURL := c.base.ResolveReference(&url.URL{
		Path: path.Join("v2", "library", modelName, "manifests", tag),
	})
	if verbose {
		fmt.Printf("Fetching manifest from: %s\n", manifestURL.String())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("performing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch manifest: %s", resp.Status)
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decoding manifest JSON: %w", err)
	}

	return &manifest, nil
}

// constructBlobURL generates the URL for downloading a blob
func constructBlobURL(base *url.URL, modelName string, digest string) string {
	blobURL := base.ResolveReference(&url.URL{
		Path: path.Join("v2", "library", modelName, "blobs", digest),
	})
	return blobURL.String()
}

// createFilename creates a filename from the digest by replacing ':' with '-'
func createFilename(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}

// updatePKGBUILD updates the source array in the PKGBUILD with new URLs and filenames
func updatePKGBUILD(urls []string, filenames []string, verbose bool) error {
	pkgbuildPath := filepath.Join(".", "PKGBUILD")
	// Read the existing PKGBUILD
	content, err := os.ReadFile(pkgbuildPath)
	if err != nil {
		return fmt.Errorf("failed to read PKGBUILD: %w", err)
	}

	// Use regex to find the source array
	reSourceArray := regexp.MustCompile(`(?ms)(source=\().*?(\))`)
	sourceArrayMatch := reSourceArray.FindSubmatchIndex(content)
	if sourceArrayMatch == nil {
		return fmt.Errorf("could not find source array in PKGBUILD")
	}

	// Build the new source array
	var newSourceArray strings.Builder
	newSourceArray.WriteString("source=(")
	for i, url := range urls {
		filename := filenames[i]
		if filename == manifestFilename {
			newSourceArray.WriteString(fmt.Sprintf("\n    '%s::%s'", filename, url))
		} else {
			newSourceArray.WriteString(fmt.Sprintf("\n    '%s'", url))
		}
	}
	newSourceArray.WriteString("\n)")

	// Replace the old source array with the new one
	newContent := append(content[:sourceArrayMatch[0]], append([]byte(newSourceArray.String()), content[sourceArrayMatch[1]:]...)...)

	// Write the updated PKGBUILD back to file
	err = os.WriteFile(pkgbuildPath, newContent, 0644)
	if err != nil {
		return fmt.Errorf("failed to write to PKGBUILD: %w", err)
	}

	if verbose {
		fmt.Println("PKGBUILD successfully updated.")
	}
	return nil
}

func main() {
	// Define flags with both long and short versions using pflag
	updateFlag := pflag.BoolP("update-pkgbuild", "u", false, "Update the ./PKGBUILD with URLs for the given model")
	verboseFlag := pflag.BoolP("verbose", "V", false, "Enable verbose output")
	versionFlag := pflag.BoolP("version", "v", false, "Show the current version")
	registryURL := pflag.StringP("registry", "r", defaultRegistry, "Registry base URL")

	pflag.Parse()

	if *versionFlag {
		fmt.Println(versionString)
		return
	}

	// Parse the registry URL
	baseURL, err := url.Parse(*registryURL)
	if err != nil {
		log.Fatalf("Error parsing registry URL '%s': %v", *registryURL, err)
	}

	// Set up HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	client := NewClient(baseURL, httpClient)

	// Define the model name (e.g., "tinyllama:latest")
	modelName := defaultModelTag
	if len(pflag.Args()) > 0 {
		modelName = pflag.Args()[0]
	} else {
		fmt.Fprintln(os.Stderr, "Please supply a model name and an optional tag as the first argument, like: tinyllama:latest")
		os.Exit(1)
	}

	// Parse the model name into repository and tag
	repository, tag := ParseModelPath(modelName)

	// Retrieve the manifest for the model with a context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	manifest, err := client.GetManifest(ctx, repository, tag, *verboseFlag)
	if err != nil {
		log.Fatalf("Error retrieving manifest: %v", err)
	}

	// Collect the blob URLs and filenames
	var blobURLs, filenames []string

	// Process the Config layer if it exists
	if manifest.Config.Digest != "" {
		if *verboseFlag {
			fmt.Printf("Processing config layer: digest = %s\n", manifest.Config.Digest)
		}
		blobURL := constructBlobURL(baseURL, repository, manifest.Config.Digest)
		filename := createFilename(manifest.Config.Digest)
		blobURLs = append(blobURLs, blobURL)
		filenames = append(filenames, filename)
	}

	// Process the Layers
	for i, layer := range manifest.Layers {
		if *verboseFlag {
			fmt.Printf("Processing layer %d: digest = %s, mediaType = %s\n", i, layer.Digest, layer.MediaType)
		}
		blobURL := constructBlobURL(baseURL, repository, layer.Digest)
		filename := createFilename(layer.Digest)
		blobURLs = append(blobURLs, blobURL)
		filenames = append(filenames, filename)
	}

	// Include the manifest
	manifestURL := baseURL.ResolveReference(&url.URL{
		Path: path.Join("v2", "library", repository, "manifests", tag),
	}).String()

	blobURLs = append(blobURLs, manifestURL)
	filenames = append(filenames, manifestFilename)

	if *updateFlag {
		if err := updatePKGBUILD(blobURLs, filenames, *verboseFlag); err != nil {
			log.Fatalf("Failed to update PKGBUILD: %v", err)
		}
		return
	}

	for i, url := range blobURLs {
		filename := filenames[i]
		if filename == manifestFilename {
			fmt.Printf("%s::%s\n", filename, url)
			continue
		}
		fmt.Printf("%s\n", url)
	}
}
