package config

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type HistogramConfig struct {
	BucketFactor    float64 `koanf:"bucketFactor"`
	ZeroThreshold   float64 `koanf:"zeroThreshold"`
	MaxBucketNumber uint32  `koanf:"maxBucketNumber"`

	Legacy LegacyHistogramConfig `koanf:"legacy"`
}

type LegacyHistogramConfig struct {
	BucketType string `koanf:"bucketType"`

	Count int `koanf:"count"`

	Start  float64 `koanf:"start"`
	Factor float64 `koanf:"factor"`

	Min float64 `koanf:"min"`
	Max float64 `koanf:"max"`

	Custom []float64 `koanf:"custom"`
}

func (c *LegacyHistogramConfig) Buckets() []float64 {
	switch c.BucketType {
	case "", "exponential":
		return prometheus.ExponentialBuckets(c.Start, c.Factor, c.Count)
	case "exponentialRange":
		return prometheus.ExponentialBucketsRange(c.Min, c.Max, c.Count)
	case "custom":
		return c.Custom
	default:
		return nil
	}
}

func (c *LegacyHistogramConfig) Enabled() bool {
	return c.BucketType != "disabled"
}

func (c *LegacyHistogramConfig) Validate() error {
	switch c.BucketType {
	case "disabled":
		return nil
	case "", "exponential":
		if c.Start <= 0 {
			return fmt.Errorf("legacy.start must be positive, got %v", c.Start)
		}
		if c.Factor <= 1 {
			return fmt.Errorf("legacy.factor must be greater than 1, got %v", c.Factor)
		}
		if c.Count < 1 {
			return fmt.Errorf("legacy.count must be at least 1, got %d", c.Count)
		}
	case "exponentialRange":
		if c.Min <= 0 {
			return fmt.Errorf("legacy.min must be positive, got %v", c.Min)
		}
		if c.Max <= c.Min {
			return fmt.Errorf("legacy.max must be greater than legacy.min, got max=%v min=%v", c.Max, c.Min)
		}
		if c.Count < 1 {
			return fmt.Errorf("legacy.count must be at least 1, got %d", c.Count)
		}
	case "custom":
		if len(c.Custom) == 0 {
			return fmt.Errorf("legacy.custom must contain at least one bucket boundary")
		}
		for i := 1; i < len(c.Custom); i++ {
			if c.Custom[i] <= c.Custom[i-1] {
				return fmt.Errorf("legacy.custom must be sorted in ascending order, got %v at index %d after %v", c.Custom[i], i, c.Custom[i-1])
			}
		}
	default:
		return fmt.Errorf("legacy.bucketType must be \"exponential\", \"exponentialRange\", \"custom\", or \"disabled\", got %q", c.BucketType)
	}
	return nil
}
