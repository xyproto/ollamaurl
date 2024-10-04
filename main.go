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
	"strings"
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

func NewClient(base *url.URL, http *http.Client) *Client {
	return &Client{
		base: base,
		http: http,
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
func (c *Client) GetManifest(ctx context.Context, modelName string, tag string) (*Manifest, error) {
	manifestURL := c.base.ResolveReference(&url.URL{
		Path: path.Join("v2", "library", modelName, "manifests", tag),
	})
	fmt.Printf("Fetching manifest from: %s\n", manifestURL.String())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	//fmt.Printf("Response status: %s\n", resp.Status)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch manifest: %s", resp.Status)
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
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

func main() {
	baseURL, _ := url.Parse("https://registry.ollama.ai") // Example base URL
	client := NewClient(baseURL, http.DefaultClient)

	// Define the model name (e.g., "tinyllama:latest")
	modelName := "tinyllama:latest"
	if len(os.Args) > 1 {
		modelName = os.Args[1]
	}

	// Parse the model name into repository and tag
	repository, tag := ParseModelPath(modelName)

	// Retrieve the manifest for the model
	manifest, err := client.GetManifest(context.Background(), repository, tag)
	if err != nil {
		log.Fatalf("Error retrieving manifest: %v", err)
	}

	// Print the blob URLs for all layers
	if len(manifest.Layers) == 0 {
		fmt.Fprintln(os.Stderr, "No layers found in the manifest.")
		os.Exit(1)
	}

	fmt.Println("Download URLs for the model:")
	for _, layer := range manifest.Layers {
		blobURL := constructBlobURL(baseURL, repository, layer.Digest)
		fmt.Println(blobURL)
	}
}
