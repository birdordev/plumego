package embedding

import (
	"fmt"
	"math"
)

// CosineSimilarity computes cosine similarity between two vectors.
// Returns a value between -1 and 1, where 1 means identical vectors.
func CosineSimilarity(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vector dimensions mismatch: %d != %d", len(a), len(b))
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0, nil
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}

// EuclideanDistance computes Euclidean distance between two vectors.
func EuclideanDistance(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vector dimensions mismatch: %d != %d", len(a), len(b))
	}

	var sum float64
	for i := 0; i < len(a); i++ {
		diff := a[i] - b[i]
		sum += diff * diff
	}

	return math.Sqrt(sum), nil
}

// DotProduct computes the dot product of two vectors.
func DotProduct(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vector dimensions mismatch: %d != %d", len(a), len(b))
	}

	var result float64
	for i := 0; i < len(a); i++ {
		result += a[i] * b[i]
	}

	return result, nil
}

// Normalize normalizes a vector to unit length.
func Normalize(v []float64) []float64 {
	var norm float64
	for _, val := range v {
		norm += val * val
	}
	norm = math.Sqrt(norm)

	if norm == 0 {
		return v
	}

	normalized := make([]float64, len(v))
	for i, val := range v {
		normalized[i] = val / norm
	}
	return normalized
}

// ManhattanDistance computes Manhattan (L1) distance between two vectors.
func ManhattanDistance(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vector dimensions mismatch: %d != %d", len(a), len(b))
	}

	var sum float64
	for i := 0; i < len(a); i++ {
		sum += math.Abs(a[i] - b[i])
	}

	return sum, nil
}

// InnerProduct computes the inner product (same as dot product).
func InnerProduct(a, b []float64) (float64, error) {
	return DotProduct(a, b)
}

// VectorNorm computes the L2 norm of a vector.
func VectorNorm(v []float64) float64 {
	var sum float64
	for _, val := range v {
		sum += val * val
	}
	return math.Sqrt(sum)
}

// BatchCosineSimilarity computes cosine similarity between a query and multiple vectors.
func BatchCosineSimilarity(query []float64, vectors [][]float64) ([]float64, error) {
	results := make([]float64, len(vectors))
	for i, vec := range vectors {
		sim, err := CosineSimilarity(query, vec)
		if err != nil {
			return nil, err
		}
		results[i] = sim
	}
	return results, nil
}

// TopK returns the indices of the top K most similar vectors.
// A nonpositive k returns an empty selection.
func TopK(similarities []float64, k int) []int {
	if k <= 0 {
		return []int{}
	}
	if k > len(similarities) {
		k = len(similarities)
	}

	// Create index array
	indices := make([]int, len(similarities))
	for i := range indices {
		indices[i] = i
	}

	// Partial sort: find top K using selection
	for i := 0; i < k; i++ {
		maxIdx := i
		for j := i + 1; j < len(similarities); j++ {
			if similarities[indices[j]] > similarities[indices[maxIdx]] {
				maxIdx = j
			}
		}
		indices[i], indices[maxIdx] = indices[maxIdx], indices[i]
	}

	return indices[:k]
}

// Interpolate performs linear interpolation between two vectors.
func Interpolate(a, b []float64, alpha float64) ([]float64, error) {
	if len(a) != len(b) {
		return nil, fmt.Errorf("vector dimensions mismatch: %d != %d", len(a), len(b))
	}

	if alpha < 0 || alpha > 1 {
		return nil, fmt.Errorf("alpha must be between 0 and 1, got %f", alpha)
	}

	result := make([]float64, len(a))
	for i := 0; i < len(a); i++ {
		result[i] = (1-alpha)*a[i] + alpha*b[i]
	}

	return result, nil
}

// AverageVectors computes the average of multiple vectors.
func AverageVectors(vectors [][]float64) ([]float64, error) {
	if len(vectors) == 0 {
		return nil, fmt.Errorf("cannot average empty vector set")
	}

	dims := len(vectors[0])
	result := make([]float64, dims)

	for _, vec := range vectors {
		if len(vec) != dims {
			return nil, fmt.Errorf("vector dimension mismatch")
		}
		for i, val := range vec {
			result[i] += val
		}
	}

	for i := range result {
		result[i] /= float64(len(vectors))
	}

	return result, nil
}
