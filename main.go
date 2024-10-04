package main

import (
	"context"
	"encoding/json"
	"flag"
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
	//fmt.Printf("Fetching manifest from: %s\n", manifestURL.String())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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

// createFilename creates a filename from the digest by replacing ':' with '-'
func createFilename(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}

// updatePKGBUILD updates the source array in the PKGBUILD with new URLs and filenames
func updatePKGBUILD(urls []string, filenames []string) error {
	pkgbuildPath := "./PKGBUILD"
	// Read the existing PKGBUILD
	content, err := os.ReadFile(pkgbuildPath)
	if err != nil {
		return fmt.Errorf("failed to read PKGBUILD: %v", err)
	}
	lines := strings.Split(string(content), "\n")

	// Remove old URLs that contain "registry.ollama.ai" from the source array
	newLines := []string{}
	inSourceArray := false
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "source=(") {
			inSourceArray = true
			newLines = append(newLines, line)
			continue
		}
		if inSourceArray {
			if strings.HasPrefix(trimmedLine, ")") {
				inSourceArray = false
				// Append new URLs with filenames
				for i, url := range urls {
					filename := filenames[i]
					newLines = append(newLines, fmt.Sprintf("    '%s::%s'", filename, url))
				}
				newLines = append(newLines, line)
				continue
			}
			// Skip old URLs containing "registry.ollama.ai"
			if strings.Contains(trimmedLine, "registry.ollama.ai") {
				continue
			}
		}
		newLines = append(newLines, line)
	}

	// Write the updated PKGBUILD back to file
	err = os.WriteFile(pkgbuildPath, []byte(strings.Join(newLines, "\n")), 0644)
	if err != nil {
		return fmt.Errorf("failed to write to PKGBUILD: %v", err)
	}

	fmt.Println("PKGBUILD successfully updated.")
	return nil
}

func main() {
	fetchFlag := flag.Bool("fetch", false, "Fetch the URLs for the given model")
	updateFlag := flag.Bool("update-pkgbuild", false, "Update the ./PKGBUILD with URLs for the given model")
	flag.Parse()

	baseURL, err := url.Parse("https://registry.ollama.ai")
	if err != nil {
		log.Fatalf("Error parsing URL: %v", err)
	}
	client := NewClient(baseURL, http.DefaultClient)

	// Define the model name (e.g., "tinyllama:latest")
	modelName := "tinyllama:latest"
	if len(flag.Args()) > 0 {
		modelName = flag.Args()[0]
	}

	// Parse the model name into repository and tag
	repository, tag := ParseModelPath(modelName)

	// Retrieve the manifest for the model
	manifest, err := client.GetManifest(context.Background(), repository, tag)
	if err != nil {
		log.Fatalf("Error retrieving manifest: %v", err)
	}

	// Collect the blob URLs and filenames
	var blobURLs, filenames []string

	for _, layer := range manifest.Layers {
		blobURL := constructBlobURL(baseURL, repository, layer.Digest)
		filename := createFilename(layer.Digest)
		blobURLs = append(blobURLs, blobURL)
		filenames = append(filenames, filename)
	}

	// Include the manifest
	manifestURL := baseURL.ResolveReference(&url.URL{
		Path: path.Join("v2", "library", repository, "manifests", tag),
	}).String()

	manifestFilename := fmt.Sprintf("%s-%s.manifest.json", repository, tag)
	blobURLs = append(blobURLs, manifestURL)
	filenames = append(filenames, manifestFilename)

	if *fetchFlag {
		fmt.Println("Download URLs for the model:")
		for i, url := range blobURLs {
			filename := filenames[i]
			fmt.Printf("%s::%s\n", filename, url)
		}
	} else if *updateFlag {
		if err := updatePKGBUILD(blobURLs, filenames); err != nil {
			log.Fatalf("Failed to update PKGBUILD: %v", err)
		}
	}
}
