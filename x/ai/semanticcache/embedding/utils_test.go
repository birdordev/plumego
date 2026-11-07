package embedding

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
		wantErr  bool
	}{
		{
			name:     "identical vectors",
			a:        []float64{1, 2, 3},
			b:        []float64{1, 2, 3},
			expected: 1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "opposite vectors",
			a:        []float64{1, 2, 3},
			b:        []float64{-1, -2, -3},
			expected: -1.0,
		},
		{
			name:    "mismatched dimensions",
			a:       []float64{1, 2},
			b:       []float64{1, 2, 3},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CosineSimilarity(tt.a, tt.b)
			if (err != nil) != tt.wantErr {
				t.Errorf("CosineSimilarity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && math.Abs(result-tt.expected) > 1e-10 {
				t.Errorf("CosineSimilarity() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestEuclideanDistance(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
		wantErr  bool
	}{
		{
			name:     "identical vectors",
			a:        []float64{1, 2, 3},
			b:        []float64{1, 2, 3},
			expected: 0.0,
		},
		{
			name:     "unit distance",
			a:        []float64{0, 0, 0},
			b:        []float64{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "3-4-5 triangle",
			a:        []float64{0, 0},
			b:        []float64{3, 4},
			expected: 5.0,
		},
		{
			name:    "mismatched dimensions",
			a:       []float64{1, 2},
			b:       []float64{1, 2, 3},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EuclideanDistance(tt.a, tt.b)
			if (err != nil) != tt.wantErr {
				t.Errorf("EuclideanDistance() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && math.Abs(result-tt.expected) > 1e-10 {
				t.Errorf("EuclideanDistance() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		v    []float64
		want func([]float64) bool
	}{
		{
			name: "simple vector",
			v:    []float64{3, 4},
			want: func(result []float64) bool {
				// Should be [0.6, 0.8]
				return math.Abs(result[0]-0.6) < 1e-10 &&
					math.Abs(result[1]-0.8) < 1e-10
			},
		},
		{
			name: "unit vector unchanged",
			v:    []float64{1, 0, 0},
			want: func(result []float64) bool {
				return math.Abs(result[0]-1.0) < 1e-10 &&
					math.Abs(result[1]-0.0) < 1e-10 &&
					math.Abs(result[2]-0.0) < 1e-10
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Normalize(tt.v)
			if !tt.want(result) {
				t.Errorf("Normalize() = %v, want matching result", result)
			}

			// Check that result is unit length
			norm := VectorNorm(result)
			if math.Abs(norm-1.0) > 1e-10 {
				t.Errorf("Normalize() result norm = %v, want 1.0", norm)
			}
		})
	}
}

func TestTopK(t *testing.T) {
	similarities := []float64{0.1, 0.9, 0.3, 0.7, 0.5}

	indices := TopK(similarities, 3)
	if len(indices) != 3 {
		t.Errorf("TopK() returned %d indices, want 3", len(indices))
	}

	// Top 3 should be indices 1, 3, 4 (values 0.9, 0.7, 0.5)
	if indices[0] != 1 || indices[1] != 3 || indices[2] != 4 {
		t.Errorf("TopK() = %v, want [1 3 4]", indices)
	}

	// Check values are in descending order
	for i := 0; i < len(indices)-1; i++ {
		if similarities[indices[i]] < similarities[indices[i+1]] {
			t.Errorf("TopK() result not in descending order")
		}
	}
}

func TestAverageVectors(t *testing.T) {
	vectors := [][]float64{
		{1.0, 2.0, 3.0},
		{2.0, 4.0, 6.0},
		{3.0, 6.0, 9.0},
	}

	result, err := AverageVectors(vectors)
	if err != nil {
		t.Fatalf("AverageVectors() error = %v", err)
	}

	expected := []float64{2.0, 4.0, 6.0}
	for i, v := range result {
		if math.Abs(v-expected[i]) > 1e-10 {
			t.Errorf("AverageVectors()[%d] = %v, want %v", i, v, expected[i])
		}
	}
}

func TestInterpolate(t *testing.T) {
	a := []float64{0.0, 0.0}
	b := []float64{10.0, 10.0}

	// Midpoint (alpha = 0.5)
	result, err := Interpolate(a, b, 0.5)
	if err != nil {
		t.Fatalf("Interpolate() error = %v", err)
	}

	expected := []float64{5.0, 5.0}
	for i, v := range result {
		if math.Abs(v-expected[i]) > 1e-10 {
			t.Errorf("Interpolate()[%d] = %v, want %v", i, v, expected[i])
		}
	}

	// Alpha = 0 should return a
	result, err = Interpolate(a, b, 0.0)
	if err != nil {
		t.Fatalf("Interpolate() error = %v", err)
	}
	for i, v := range result {
		if math.Abs(v-a[i]) > 1e-10 {
			t.Errorf("Interpolate(alpha=0)[%d] = %v, want %v", i, v, a[i])
		}
	}

	// Alpha = 1 should return b
	result, err = Interpolate(a, b, 1.0)
	if err != nil {
		t.Fatalf("Interpolate() error = %v", err)
	}
	for i, v := range result {
		if math.Abs(v-b[i]) > 1e-10 {
			t.Errorf("Interpolate(alpha=1)[%d] = %v, want %v", i, v, b[i])
		}
	}
}

func BenchmarkCosineSimilarity(b *testing.B) {
	vec1 := make([]float64, 1536)
	vec2 := make([]float64, 1536)
	for i := range vec1 {
		vec1[i] = float64(i)
		vec2[i] = float64(i) * 0.9
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = CosineSimilarity(vec1, vec2)
	}
}

func BenchmarkNormalize(b *testing.B) {
	vec := make([]float64, 1536)
	for i := range vec {
		vec[i] = float64(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Normalize(vec)
	}
}
