package controller

import (
	api "anvil.dev/operator/api/v1alpha1"
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"time"
)

// Aggregate counts and oldest ages avoid resource names, input values, and UID labels.
var runCounts = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "anvil_training_runs", Help: "Current training runs by phase in the configured namespace."}, []string{"phase"})
var oldestRun = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "anvil_training_oldest_wait_seconds", Help: "Oldest active cancellation, capacity wait, or deletion age."}, []string{"reason"})
var observationErrors = prometheus.NewCounter(prometheus.CounterOpts{Name: "anvil_metrics_observation_errors_total", Help: "Failed training inventory observations."})

func init() { metrics.Registry.MustRegister(runCounts, oldestRun, observationErrors) }

type TrainingMetrics struct {
	Reader    client.Reader
	Namespace string
}

func (*TrainingMetrics) NeedLeaderElection() bool { return true }
func (m *TrainingMetrics) Start(ctx context.Context) error {
	timer := time.NewTicker(15 * time.Second)
	defer timer.Stop()
	for {
		m.observe(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}
func (m *TrainingMetrics) observe(ctx context.Context) {
	var runs api.TrainingRunList
	if err := m.Reader.List(ctx, &runs, client.InNamespace(m.Namespace)); err != nil {
		observationErrors.Inc()
		return
	}
	counts := map[string]float64{"Pending": 0, "Submitting": 0, "Running": 0, "Cancelling": 0, "Succeeded": 0, "Failed": 0, "Cancelled": 0}
	ages := map[string]float64{"cancellation": 0, "capacity": 0, "finalizer": 0}
	now := time.Now()
	for _, run := range runs.Items {
		phase := run.Status.Phase
		if phase == "" {
			phase = "Pending"
		}
		if _, known := counts[phase]; known {
			counts[phase]++
		}
		if !run.DeletionTimestamp.IsZero() {
			ages["finalizer"] = max(ages["finalizer"], now.Sub(run.DeletionTimestamp.Time).Seconds())
		}
		for _, condition := range run.Status.Conditions {
			age := max(0, now.Sub(condition.LastTransitionTime.Time).Seconds())
			if phase == "Cancelling" && condition.Type == "CancellationRequested" && condition.Status == "True" {
				ages["cancellation"] = max(ages["cancellation"], age)
			}
			if !terminal(phase) && condition.Type == "CapacityAvailable" && condition.Status == "False" {
				ages["capacity"] = max(ages["capacity"], age)
			}
		}
	}
	for phase, count := range counts {
		runCounts.WithLabelValues(phase).Set(count)
	}
	for reason, age := range ages {
		oldestRun.WithLabelValues(reason).Set(age)
	}
}
