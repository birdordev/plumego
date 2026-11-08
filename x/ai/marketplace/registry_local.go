package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/birdordev/plumego/internal/semver"
)

// LocalAgentRegistry implements AgentRegistry with local file system storage.
type LocalAgentRegistry struct {
	baseDir string
	mu      sync.RWMutex
}

// NewLocalAgentRegistry creates a new local agent registry.
// Default base directory is ~/.plumego/agents
func NewLocalAgentRegistry(baseDir string) (*LocalAgentRegistry, error) {
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, ".plumego", "agents")
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &LocalAgentRegistry{
		baseDir: baseDir,
	}, nil
}

// Publish publishes an agent to the registry.
func (r *LocalAgentRegistry) Publish(ctx context.Context, metadata *AgentMetadata) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate version
	if _, err := semver.Parse(metadata.Version); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidVersion, err)
	}

	// Create directory structure
	agentDir := filepath.Join(r.baseDir, metadata.ID, metadata.Version)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory: %w", err)
	}

	// Check if already exists
	metadataPath := filepath.Join(agentDir, "metadata.json")
	if _, err := os.Stat(metadataPath); err == nil {
		return ErrDuplicateAgent
	}

	// Save metadata
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	return nil
}

// Get retrieves agent metadata by ID and version.
func (r *LocalAgentRegistry) Get(ctx context.Context, id string, ver string) (*AgentMetadata, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.getLocked(id, ver)
}

// getLocked is the lock-free body of Get; callers must hold at least r.mu.RLock.
func (r *LocalAgentRegistry) getLocked(id, ver string) (*AgentMetadata, error) {
	metadataPath := filepath.Join(r.baseDir, id, ver, "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrAgentNotFound
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata AgentMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &metadata, nil
}

// Search searches for agents matching the query.
func (r *LocalAgentRegistry) Search(ctx context.Context, query SearchQuery) ([]*AgentMetadata, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []*AgentMetadata

	// Walk through agent directories
	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, fmt.Errorf("failed to read base directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		agentID := entry.Name()
		versions, err := r.ListVersions(ctx, agentID)
		if err != nil {
			continue
		}

		// Get latest version
		if len(versions) == 0 {
			continue
		}
		latestVersion := versions[len(versions)-1]

		metadata, err := r.Get(ctx, agentID, latestVersion)
		if err != nil {
			continue
		}

		// Apply filters
		if !r.matchesQuery(metadata, query) {
			continue
		}

		results = append(results, metadata)
	}

	// Sort by rating (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Rating > results[j].Rating
	})

	// Apply limit
	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return results, nil
}

// matchesQuery checks if metadata matches search query.
func (r *LocalAgentRegistry) matchesQuery(metadata *AgentMetadata, query SearchQuery) bool {
	// Category filter
	if query.Category != "" && metadata.Category != query.Category {
		return false
	}

	// Tags filter
	if len(query.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, tag := range metadata.Tags {
			tagSet[tag] = true
		}
		for _, queryTag := range query.Tags {
			if !tagSet[queryTag] {
				return false
			}
		}
	}

	// Provider filter
	if query.Provider != "" && metadata.Provider != query.Provider {
		return false
	}

	// Text search (name, description, author)
	if query.Text != "" {
		lowerText := strings.ToLower(query.Text)
		if !strings.Contains(strings.ToLower(metadata.Name), lowerText) &&
			!strings.Contains(strings.ToLower(metadata.Description), lowerText) &&
			!strings.Contains(strings.ToLower(metadata.Author), lowerText) {
			return false
		}
	}

	return true
}

// ListVersions lists all versions of an agent.
func (r *LocalAgentRegistry) ListVersions(ctx context.Context, id string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.listVersionsLocked(id)
}

// listVersionsLocked is the lock-free body of ListVersions; callers must hold at least r.mu.RLock.
func (r *LocalAgentRegistry) listVersionsLocked(id string) ([]string, error) {
	agentDir := filepath.Join(r.baseDir, id)
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read agent directory: %w", err)
	}

	var versions []*semver.Version
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		v, err := semver.Parse(entry.Name())
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}

	semver.Sort(versions)

	var result []string
	for _, v := range versions {
		result = append(result, v.String())
	}

	return result, nil
}

// Delete removes an agent from the registry.
func (r *LocalAgentRegistry) Delete(ctx context.Context, id string, ver string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agentDir := filepath.Join(r.baseDir, id, ver)
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		return ErrAgentNotFound
	}

	if err := os.RemoveAll(agentDir); err != nil {
		return fmt.Errorf("failed to delete agent: %w", err)
	}

	// Clean up empty parent directory
	parentDir := filepath.Join(r.baseDir, id)
	entries, err := os.ReadDir(parentDir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(parentDir)
	}

	return nil
}

// Rate adds a rating for an agent.
func (r *LocalAgentRegistry) Rate(ctx context.Context, rating *Rating) error {
	r.mu.Lock()

	ratingsDir := filepath.Join(r.baseDir, rating.TargetID, "ratings")
	if err := os.MkdirAll(ratingsDir, 0755); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("failed to create ratings directory: %w", err)
	}

	ratingPath := filepath.Join(ratingsDir, fmt.Sprintf("%s.json", rating.UserID))
	data, err := json.MarshalIndent(rating, "", "  ")
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("failed to marshal rating: %w", err)
	}

	if err := os.WriteFile(ratingPath, data, 0644); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("failed to write rating: %w", err)
	}

	r.mu.Unlock()

	// Update aggregate rating (unlocked, so it can acquire its own locks)
	return r.updateAggregateRating(ctx, rating.TargetID)
}

// GetRatings retrieves ratings for an agent.
func (r *LocalAgentRegistry) GetRatings(ctx context.Context, agentID string) ([]*Rating, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ratingsDir := filepath.Join(r.baseDir, agentID, "ratings")
	entries, err := os.ReadDir(ratingsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Rating{}, nil
		}
		return nil, fmt.Errorf("failed to read ratings directory: %w", err)
	}

	var ratings []*Rating
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(ratingsDir, entry.Name()))
		if err != nil {
			continue
		}

		var rating Rating
		if err := json.Unmarshal(data, &rating); err != nil {
			continue
		}

		ratings = append(ratings, &rating)
	}

	return ratings, nil
}

// updateAggregateRating updates the aggregate rating for an agent.
func (r *LocalAgentRegistry) updateAggregateRating(ctx context.Context, agentID string) error {
	ratings, err := r.GetRatings(ctx, agentID)
	if err != nil {
		return err
	}

	if len(ratings) == 0 {
		return nil
	}

	var sum float64
	for _, rating := range ratings {
		sum += rating.Score
	}
	avgRating := sum / float64(len(ratings))

	// Update all versions with new aggregate rating
	versions, err := r.ListVersions(ctx, agentID)
	if err != nil {
		return err
	}

	for _, ver := range versions {
		metadata, err := r.Get(ctx, agentID, ver)
		if err != nil {
			continue
		}

		metadata.Rating = avgRating

		metadataPath := filepath.Join(r.baseDir, agentID, ver, "metadata.json")
		data, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			continue
		}

		_ = os.WriteFile(metadataPath, data, 0644)
	}

	return nil
}

// ResolveDependencies resolves all dependencies for an agent.
func (r *LocalAgentRegistry) ResolveDependencies(ctx context.Context, id string, ver string) ([]*AgentMetadata, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	visited := make(map[string]bool)
	var resolved []*AgentMetadata

	if err := r.resolveDependenciesRecursive(ctx, id, ver, visited, &resolved); err != nil {
		return nil, err
	}

	return resolved, nil
}

// resolveDependenciesRecursive recursively resolves dependencies.
func (r *LocalAgentRegistry) resolveDependenciesRecursive(
	ctx context.Context,
	id string,
	ver string,
	visited map[string]bool,
	resolved *[]*AgentMetadata,
) error {
	key := fmt.Sprintf("%s@%s", id, ver)
	if visited[key] {
		return nil
	}
	visited[key] = true

	metadata, err := r.Get(ctx, id, ver)
	if err != nil {
		return fmt.Errorf("%w: %s@%s", ErrDependencyNotFound, id, ver)
	}

	// Resolve dependencies first (depth-first)
	for _, dep := range metadata.Dependencies {
		parts := strings.Split(dep, "@")
		if len(parts) != 2 {
			return fmt.Errorf("invalid dependency format: %s", dep)
		}

		depID, depVer := parts[0], parts[1]
		if err := r.resolveDependenciesRecursive(ctx, depID, depVer, visited, resolved); err != nil {
			return err
		}
	}

	// Add current agent
	*resolved = append(*resolved, metadata)
	return nil
}

// UpdateDownloadCount increments download counter.
func (r *LocalAgentRegistry) UpdateDownloadCount(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	versions, err := r.listVersionsLocked(id)
	if err != nil {
		return err
	}

	if len(versions) == 0 {
		return ErrAgentNotFound
	}

	// Update latest version
	latestVersion := versions[len(versions)-1]
	metadata, err := r.getLocked(id, latestVersion)
	if err != nil {
		return err
	}

	metadata.Downloads++

	metadataPath := filepath.Join(r.baseDir, id, latestVersion, "metadata.json")
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	return nil
}

// LocalWorkflowRegistry implements WorkflowRegistry with local file system storage.
type LocalWorkflowRegistry struct {
	baseDir string
	mu      sync.RWMutex
}

// NewLocalWorkflowRegistry creates a new local workflow registry.
// Default base directory is ~/.plumego/workflows
func NewLocalWorkflowRegistry(baseDir string) (*LocalWorkflowRegistry, error) {
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, ".plumego", "workflows")
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &LocalWorkflowRegistry{
		baseDir: baseDir,
	}, nil
}

// Publish publishes a workflow template to the registry.
func (r *LocalWorkflowRegistry) Publish(ctx context.Context, template *WorkflowTemplate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate version
	if _, err := semver.Parse(template.Version); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidVersion, err)
	}

	// Create directory structure
	workflowDir := filepath.Join(r.baseDir, template.ID, template.Version)
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("failed to create workflow directory: %w", err)
	}

	// Check if already exists
	templatePath := filepath.Join(workflowDir, "template.json")
	if _, err := os.Stat(templatePath); err == nil {
		return ErrDuplicateWorkflow
	}

	// Save template
	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal template: %w", err)
	}

	if err := os.WriteFile(templatePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write template: %w", err)
	}

	return nil
}

// Get retrieves workflow template by ID and version.
func (r *LocalWorkflowRegistry) Get(ctx context.Context, id string, ver string) (*WorkflowTemplate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	templatePath := filepath.Join(r.baseDir, id, ver, "template.json")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrWorkflowNotFound
		}
		return nil, fmt.Errorf("failed to read template: %w", err)
	}

	var template WorkflowTemplate
	if err := json.Unmarshal(data, &template); err != nil {
		return nil, fmt.Errorf("failed to unmarshal template: %w", err)
	}

	return &template, nil
}

// Search searches for workflows matching the query.
func (r *LocalWorkflowRegistry) Search(ctx context.Context, query SearchQuery) ([]*WorkflowTemplate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []*WorkflowTemplate

	// Walk through workflow directories
	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, fmt.Errorf("failed to read base directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workflowID := entry.Name()
		versions, err := r.ListVersions(ctx, workflowID)
		if err != nil {
			continue
		}

		// Get latest version
		if len(versions) == 0 {
			continue
		}
		latestVersion := versions[len(versions)-1]

		template, err := r.Get(ctx, workflowID, latestVersion)
		if err != nil {
			continue
		}

		// Apply filters
		if !r.matchesQuery(template, query) {
			continue
		}

		results = append(results, template)
	}

	// Sort by rating (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Rating > results[j].Rating
	})

	// Apply limit
	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return results, nil
}

// matchesQuery checks if template matches search query.
func (r *LocalWorkflowRegistry) matchesQuery(template *WorkflowTemplate, query SearchQuery) bool {
	// Category filter
	if query.Category != "" && string(template.Category) != string(query.Category) {
		return false
	}

	// Tags filter
	if len(query.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, tag := range template.Tags {
			tagSet[tag] = true
		}
		for _, queryTag := range query.Tags {
			if !tagSet[queryTag] {
				return false
			}
		}
	}

	// Text search (name, description, author)
	if query.Text != "" {
		lowerText := strings.ToLower(query.Text)
		if !strings.Contains(strings.ToLower(template.Name), lowerText) &&
			!strings.Contains(strings.ToLower(template.Description), lowerText) &&
			!strings.Contains(strings.ToLower(template.Author), lowerText) {
			return false
		}
	}

	return true
}

// ListVersions lists all versions of a workflow.
func (r *LocalWorkflowRegistry) ListVersions(ctx context.Context, id string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflowDir := filepath.Join(r.baseDir, id)
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read workflow directory: %w", err)
	}

	var versions []*semver.Version
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		v, err := semver.Parse(entry.Name())
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}

	// Sort versions
	semver.Sort(versions)

	// Convert to strings
	var result []string
	for _, v := range versions {
		result = append(result, v.String())
	}

	return result, nil
}

// Delete removes a workflow from the registry.
func (r *LocalWorkflowRegistry) Delete(ctx context.Context, id string, ver string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	workflowDir := filepath.Join(r.baseDir, id, ver)
	if _, err := os.Stat(workflowDir); os.IsNotExist(err) {
		return ErrWorkflowNotFound
	}

	if err := os.RemoveAll(workflowDir); err != nil {
		return fmt.Errorf("failed to delete workflow: %w", err)
	}

	// Clean up empty parent directory
	parentDir := filepath.Join(r.baseDir, id)
	entries, err := os.ReadDir(parentDir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(parentDir)
	}

	return nil
}

// Rate adds a rating for a workflow.
func (r *LocalWorkflowRegistry) Rate(ctx context.Context, rating *Rating) error {
	r.mu.Lock()

	ratingsDir := filepath.Join(r.baseDir, rating.TargetID, "ratings")
	if err := os.MkdirAll(ratingsDir, 0755); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("failed to create ratings directory: %w", err)
	}

	ratingPath := filepath.Join(ratingsDir, fmt.Sprintf("%s.json", rating.UserID))
	data, err := json.MarshalIndent(rating, "", "  ")
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("failed to marshal rating: %w", err)
	}

	if err := os.WriteFile(ratingPath, data, 0644); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("failed to write rating: %w", err)
	}

	r.mu.Unlock()

	// Update aggregate rating (unlocked, so it can acquire its own locks)
	return r.updateAggregateRating(ctx, rating.TargetID)
}

// GetRatings retrieves ratings for a workflow.
func (r *LocalWorkflowRegistry) GetRatings(ctx context.Context, workflowID string) ([]*Rating, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ratingsDir := filepath.Join(r.baseDir, workflowID, "ratings")
	entries, err := os.ReadDir(ratingsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Rating{}, nil
		}
		return nil, fmt.Errorf("failed to read ratings directory: %w", err)
	}

	var ratings []*Rating
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(ratingsDir, entry.Name()))
		if err != nil {
			continue
		}

		var rating Rating
		if err := json.Unmarshal(data, &rating); err != nil {
			continue
		}

		ratings = append(ratings, &rating)
	}

	return ratings, nil
}

// updateAggregateRating updates the aggregate rating for a workflow.
func (r *LocalWorkflowRegistry) updateAggregateRating(ctx context.Context, workflowID string) error {
	ratings, err := r.GetRatings(ctx, workflowID)
	if err != nil {
		return err
	}

	if len(ratings) == 0 {
		return nil
	}

	var sum float64
	for _, rating := range ratings {
		sum += rating.Score
	}
	avgRating := sum / float64(len(ratings))

	// Update all versions with new aggregate rating
	versions, err := r.ListVersions(ctx, workflowID)
	if err != nil {
		return err
	}

	for _, ver := range versions {
		template, err := r.Get(ctx, workflowID, ver)
		if err != nil {
			continue
		}

		template.Rating = avgRating

		templatePath := filepath.Join(r.baseDir, workflowID, ver, "template.json")
		data, err := json.MarshalIndent(template, "", "  ")
		if err != nil {
			continue
		}

		_ = os.WriteFile(templatePath, data, 0644)
	}

	return nil
}
