package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jfrog/jfrog-cli-core/v2/plugins/components"
)

// PackageInfo represents package information
type PackageInfo struct {
	Version    string                 `json:"version"`
	Type       string                 `json:"type"`
	Resolution map[string]interface{} `json:"resolution"`
	Engines    map[string]interface{} `json:"engines"`
}

// Dependency represents a dependency to be audited
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

// DependencyTree represents the complete dependency tree
type DependencyTree struct {
	Packages map[string]PackageInfo `json:"packages"`
}

// LockData represents the structure of pnpm-lock.yaml
type LockData struct {
	Packages map[string]map[string]interface{} `yaml:"packages"`
}

// AuditResult represents the result of a single package audit
type AuditResult struct {
	Index      int
	Name       string
	Version    string
	Type       string
	Status     string
	StatusCode int
	Error      error
}

func extractIndirectDependencies(versionString string) map[string]PackageInfo {
	indirectDeps := make(map[string]PackageInfo)

	// Pattern to match (package@version) in the version string
	pattern := regexp.MustCompile(`\(([^@]+)@([^)]+)\)`)
	matches := pattern.FindAllStringSubmatch(versionString, -1)

	for _, match := range matches {
		if len(match) == 3 {
			packageName := match[1]
			packageVersion := match[2]
			indirectDeps[packageName] = PackageInfo{
				Version: packageVersion,
				Type:    "indirect",
			}
		}
	}

	return indirectDeps
}

func parsePackageKey(packageKey string) (string, string) {
	// Handle scoped packages like '@cypress/listr-verbose-renderer@0.4.1'
	if strings.HasPrefix(packageKey, "@") {
		// Find the last @ symbol which separates package name from version
		lastAtIndex := strings.LastIndex(packageKey, "@")
		if lastAtIndex > 0 {
			packageName := packageKey[:lastAtIndex]
			version := packageKey[lastAtIndex+1:]
			return packageName, version
		}
	} else {
		// Handle regular packages like 'abbrev@1.1.1'
		parts := strings.SplitN(packageKey, "@", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}

	return "", ""
}

func parsePnpmLock(lockFilePath string) (*DependencyTree, error) {
	// Check if the specified file exists
	if _, err := os.Stat(lockFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("pnpm-lock.yaml not found at path: %s", lockFilePath)
	}

	// Read the YAML file
	data, err := ioutil.ReadFile(lockFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading %s: %v", lockFilePath, err)
	}

	// Parse YAML using the yaml.v3 library
	var lockData LockData
	if err := yaml.Unmarshal(data, &lockData); err != nil {
		return nil, fmt.Errorf("error parsing YAML: %v", err)
	}

	allPackages := make(map[string]PackageInfo)

	// Process packages section
	for packageKey, packageInfo := range lockData.Packages {
		packageName, version := parsePackageKey(packageKey)
		if packageName != "" && version != "" {
			info := PackageInfo{
				Version: version,
				Type:    "package",
			}

			// Extract resolution and engines if they exist
			if resolution, exists := packageInfo["resolution"]; exists {
				if resMap, ok := resolution.(map[string]interface{}); ok {
					info.Resolution = resMap
				}
			}
			if engines, exists := packageInfo["engines"]; exists {
				if engMap, ok := engines.(map[string]interface{}); ok {
					info.Engines = engMap
				}
			}

			allPackages[packageName] = info
		}
	}

	return &DependencyTree{
		Packages: allPackages,
	}, nil
}

func saveDependencyTree(dependencies *DependencyTree, outputPath string) error {
	// Convert to JSON
	jsonData, err := json.MarshalIndent(dependencies, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling JSON: %v", err)
	}

	// Write to file
	if err := ioutil.WriteFile(outputPath, jsonData, 0644); err != nil {
		return fmt.Errorf("error writing JSON file: %v", err)
	}

	fmt.Printf("PNPM dependency tree saved to %s\n", outputPath)
	return nil
}

func fetchDependenciesFromTree(dependencies *DependencyTree) ([]Dependency, error) {
	var deps []Dependency

	// Get all package names and sort them for consistent ordering
	var packageNames []string
	for packageName := range dependencies.Packages {
		packageNames = append(packageNames, packageName)
	}
	sort.Strings(packageNames)

	// Process packages section in sorted order
	for _, packageName := range packageNames {
		info := dependencies.Packages[packageName]
		deps = append(deps, Dependency{
			Name:    packageName,
			Version: info.Version,
			Type:    info.Type,
		})
	}

	return deps, nil
}

func checkNpmRegistry(packageName, packageVersion, packageType, npmRegistryBaseURL, accessToken string) AuditResult {
	// Handle scoped packages (starting with @)
	var packageURL string
	if strings.HasPrefix(packageName, "@") {
		// For scoped packages: @scope/package -> @scope/package/-/package-version.tgz
		parts := strings.Split(packageName, "/")
		if len(parts) >= 2 {
			packageNameOnly := parts[len(parts)-1]
			packageURL = fmt.Sprintf("%s/%s/-/%s-%s.tgz", npmRegistryBaseURL, packageName, packageNameOnly, packageVersion)
		} else {
			return AuditResult{
				Name:    packageName,
				Version: packageVersion,
				Type:    packageType,
				Status:  "❌ Invalid scoped package format",
				Error:   fmt.Errorf("invalid scoped package format"),
			}
		}
	} else {
		// For regular packages: package -> package/-/package-version.tgz
		packageURL = fmt.Sprintf("%s/%s/-/%s-%s.tgz", npmRegistryBaseURL, packageName, packageName, packageVersion)
	}

	// Create HTTP client with shorter timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request
	req, err := http.NewRequest("GET", packageURL, nil)
	if err != nil {
		return AuditResult{
			Name:    packageName,
			Version: packageVersion,
			Type:    packageType,
			Status:  "❌ Request Failed",
			Error:   err,
		}
	}

	// Add authorization header if token provided
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return AuditResult{
			Name:    packageName,
			Version: packageVersion,
			Type:    packageType,
			Status:  "❌ Request Failed",
			Error:   err,
		}
	}
	defer resp.Body.Close()

	// Check response status
	var status string
	switch resp.StatusCode {
	case http.StatusOK:
		status = "✅ Available in NPM Registry"
	case http.StatusForbidden:
		status = "❌ Blocked (403 Forbidden)"
	case http.StatusNotFound:
		status = "❌ Not Found (404)"
	default:
		status = fmt.Sprintf("⚠️ Unexpected Response: %d", resp.StatusCode)
	}

	return AuditResult{
		Name:       packageName,
		Version:    packageVersion,
		Type:       packageType,
		Status:     status,
		StatusCode: resp.StatusCode,
	}
}

func worker(id int, jobs <-chan Dependency, results chan<- AuditResult, npmRegistryBaseURL, accessToken string, wg *sync.WaitGroup) {
	defer wg.Done()

	for dep := range jobs {
		result := checkNpmRegistry(dep.Name, dep.Version, dep.Type, npmRegistryBaseURL, accessToken)
		results <- result
	}
}

func auditDependenciesConcurrently(deps []Dependency, npmRegistryBaseURL, accessToken string, numWorkers int) {
	// Create channels for jobs and results
	jobs := make(chan Dependency, len(deps))
	results := make(chan AuditResult, len(deps))

	// Create worker pool
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go worker(i, jobs, results, npmRegistryBaseURL, accessToken, &wg)
	}

	// Send jobs to workers
	go func() {
		for _, dep := range deps {
			depCopy := dep // Create a copy to avoid closure issues
			jobs <- depCopy
		}
		close(jobs)
	}()

	// Collect results as they come in
	go func() {
		wg.Wait()
		close(results)
	}()

	// Process results in order
	resultMap := make(map[int]AuditResult)
	completed := 0

	for result := range results {
		// Find the original index of this dependency
		for i, dep := range deps {
			if dep.Name == result.Name && dep.Version == result.Version {
				result.Index = i
				resultMap[i] = result
				break
			}
		}
		completed++

		// Print progress
		fmt.Printf("\rProgress: %d/%d packages checked", completed, len(deps))
	}

	fmt.Println() // New line after progress

	// Print results in original order
	for i := 0; i < len(deps); i++ {
		if result, exists := resultMap[i]; exists {
			fmt.Printf("\n[%d/%d] %s@%s (%s) %s",
				i+1, len(deps), result.Name, result.Version, result.Type, result.Status)
			if result.Error != nil {
				fmt.Printf(" - Error: %v", result.Error)
			}
		}
	}
}

func getApp() components.App {
	app := components.CreateApp(
		// Plugin namespace prefix (command usage: app <cmd-name>)
		"ca-extension",
		// Plugin version vX.X.X
		"v1.0.0",
		// Plugin description for help usage
		"description",
		// Plugin commands
		getCommands(),
	)
	return app
}

func getCommands() []components.Command {
	return []components.Command{
		{
			Name:        "pnpm",
			Description: "Curation Audit for pnpm",
			Action:      GreetCmd,
		},
	}
}

func GreetCmd(c *components.Context) (err error) {
	log.Println("Hello World") //.info("Hello World")

	return
}

func start() {

}

func main() {

	//plugins.PluginMain(getApp())

	// Check command line arguments
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run scripts/combined_audit/main.go <PNPM_LOCK_FILE> <NPM_REGISTRY_BASE_URL> [ACCESS_TOKEN] [NUM_WORKERS]")
		fmt.Println("Example: go run scripts/combined_audit/main.go \"pnpm-lock.yaml\" \"https://registry.npmjs.org\" \"$MY_ACCESS_TOKEN\" 10")
		fmt.Println("Note: ACCESS_TOKEN and NUM_WORKERS are optional (default: no token, 5 workers)")
		os.Exit(1)
	}

	lockFilePath := os.Args[1]
	npmRegistryBaseURL := os.Args[2]
	accessToken := ""
	numWorkers := 5 // Default number of workers

	if len(os.Args) > 3 {
		accessToken = os.Args[3]
	}

	if len(os.Args) > 4 {
		if workers, err := fmt.Sscanf(os.Args[4], "%d", &numWorkers); err != nil || workers != 1 {
			fmt.Printf("Warning: Invalid number of workers '%s', using default of 5\n", os.Args[4])
			numWorkers = 5
		}
	}

	fmt.Printf("PNPM Lock File: %s\n", lockFilePath)
	fmt.Printf("NPM Registry Base URL: %s\n", npmRegistryBaseURL)
	fmt.Printf("Number of Workers: %d\n", numWorkers)

	// Step 1: Parse pnpm-lock.yaml
	fmt.Println("\n=== Step 1: Parsing pnpm-lock.yaml ===")
	dependencies, err := parsePnpmLock(lockFilePath)
	if err != nil {
		log.Fatalf("Error parsing pnpm-lock.yaml: %v", err)
	}

	// Step 2: Save dependency tree to JSON
	fmt.Println("\n=== Step 2: Saving dependency tree ===")
	outputDir := filepath.Dir(lockFilePath)
	outputPath := filepath.Join(outputDir, "pnpm_dependency_tree.json")

	if err := saveDependencyTree(dependencies, outputPath); err != nil {
		log.Fatalf("Error saving dependency tree: %v", err)
	}

	// Step 3: Fetch dependencies for auditing
	fmt.Println("\n=== Step 3: Preparing for audit ===")
	deps, err := fetchDependenciesFromTree(dependencies)
	if err != nil {
		log.Fatalf("Error preparing dependencies for audit: %v", err)
	}

	fmt.Printf("Found %d dependencies to audit\n", len(deps))

	// Step 4: Audit dependencies against npm registry (concurrent)
	fmt.Println("\n=== Step 4: Auditing dependencies (concurrent) ===")
	startTime := time.Now()

	auditDependenciesConcurrently(deps, npmRegistryBaseURL, accessToken, numWorkers)

	duration := time.Since(startTime)
	fmt.Printf("\n=== Audit Complete ===\n")
	fmt.Printf("Processed %d dependencies from %s\n", len(deps), lockFilePath)
	fmt.Printf("Dependency tree saved to: %s\n", outputPath)
	fmt.Printf("Total time: %v\n", duration)
}
