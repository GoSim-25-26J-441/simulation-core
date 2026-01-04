package simd

import (
	"context"
	"errors"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SimulationGRPCServer implements the gRPC SimulationServiceServer using a RunStore backend.
type SimulationGRPCServer struct {
	simulationv1.UnimplementedSimulationServiceServer
	store    *RunStore
	Executor *RunExecutor
}

// NewSimulationGRPCServer creates a new SimulationGRPCServer with the provided RunStore and RunExecutor.
func NewSimulationGRPCServer(store *RunStore, executor *RunExecutor) *SimulationGRPCServer {
	return &SimulationGRPCServer{
		store:    store,
		Executor: executor,
	}
}

func (s *SimulationGRPCServer) CreateRun(ctx context.Context, req *simulationv1.CreateRunRequest) (*simulationv1.CreateRunResponse, error) {
	if req == nil || req.Input == nil {
		return nil, status.Error(codes.InvalidArgument, "input is required")
	}

	rec, err := s.store.Create(req.RunId, req.Input)
	if err != nil {
		return nil, status.Error(codes.AlreadyExists, err.Error())
	}

	logger.Info("run created", "run_id", rec.Run.Id)
	return &simulationv1.CreateRunResponse{Run: rec.Run}, nil
}

func (s *SimulationGRPCServer) StartRun(ctx context.Context, req *simulationv1.StartRunRequest) (*simulationv1.StartRunResponse, error) {
	if req == nil || req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id is required")
	}

	updated, err := s.Executor.Start(req.RunId)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if errors.Is(err, ErrRunTerminal) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Info("run started (executor)", "run_id", req.RunId)
	return &simulationv1.StartRunResponse{Run: updated.Run}, nil
}

func (s *SimulationGRPCServer) StopRun(ctx context.Context, req *simulationv1.StopRunRequest) (*simulationv1.StopRunResponse, error) {
	if req == nil || req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id is required")
	}

	updated, err := s.Executor.Stop(req.RunId)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if errors.Is(err, ErrRunIDMissing) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Info("run cancelled", "run_id", req.RunId)
	return &simulationv1.StopRunResponse{Run: updated.Run}, nil
}

func (s *SimulationGRPCServer) GetRun(ctx context.Context, req *simulationv1.GetRunRequest) (*simulationv1.GetRunResponse, error) {
	if req == nil || req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id is required")
	}
	rec, ok := s.store.Get(req.RunId)
	if !ok {
		return nil, status.Error(codes.NotFound, "run not found")
	}
	return &simulationv1.GetRunResponse{Run: rec.Run}, nil
}

func (s *SimulationGRPCServer) ListRuns(ctx context.Context, req *simulationv1.ListRunsRequest) (*simulationv1.ListRunsResponse, error) {
	limit := 50
	if req != nil && req.Limit > 0 {
		limit = int(req.Limit)
	}
	recs := s.store.List(limit)
	runs := make([]*simulationv1.Run, 0, len(recs))
	for _, rec := range recs {
		runs = append(runs, rec.Run)
	}
	return &simulationv1.ListRunsResponse{Runs: runs}, nil
}

func (s *SimulationGRPCServer) GetRunMetrics(ctx context.Context, req *simulationv1.GetRunMetricsRequest) (*simulationv1.GetRunMetricsResponse, error) {
	if req == nil || req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id is required")
	}
	rec, ok := s.store.Get(req.RunId)
	if !ok {
		return nil, status.Error(codes.NotFound, "run not found")
	}
	if rec.Metrics == nil {
		return nil, status.Error(codes.FailedPrecondition, "metrics not available")
	}
	return &simulationv1.GetRunMetricsResponse{Metrics: rec.Metrics}, nil
}

func (s *SimulationGRPCServer) StreamRunEvents(req *simulationv1.StreamRunEventsRequest, stream simulationv1.SimulationService_StreamRunEventsServer) error {
	if req == nil || req.RunId == "" {
		return status.Error(codes.InvalidArgument, "run_id is required")
	}

	rec, ok := s.store.Get(req.RunId)
	if !ok {
		return status.Error(codes.NotFound, "run not found")
	}

	// Send initial status event
	at := time.Now().UTC().UnixMilli()
	previousStatus := simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED
	if err := stream.Send(&simulationv1.StreamRunEventsResponse{Event: &simulationv1.RunEvent{
		AtUnixMs: at,
		RunId:    req.RunId,
		Event: &simulationv1.RunEvent_StatusChanged{
			StatusChanged: &simulationv1.RunStatusChanged{
				Previous: previousStatus,
				Current:  rec.Run.Status,
			},
		},
	}}); err != nil {
		return err
	}
	previousStatus = rec.Run.Status

	// Poll for status changes and metrics updates
	interval := 500 * time.Millisecond
	if req.MetricsIntervalMs > 0 {
		interval = time.Duration(req.MetricsIntervalMs) * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			rec, ok := s.store.Get(req.RunId)
			if !ok {
				return status.Error(codes.NotFound, "run not found")
			}

			// Check for status changes
			if rec.Run.Status != previousStatus {
				if err := stream.Send(&simulationv1.StreamRunEventsResponse{Event: &simulationv1.RunEvent{
					AtUnixMs: time.Now().UTC().UnixMilli(),
					RunId:    req.RunId,
					Event: &simulationv1.RunEvent_StatusChanged{
						StatusChanged: &simulationv1.RunStatusChanged{
							Previous: previousStatus,
							Current:  rec.Run.Status,
						},
					},
				}}); err != nil {
					return err
				}
				previousStatus = rec.Run.Status
			}

			// Send metrics snapshot if available
			if rec.Metrics != nil {
				if err := stream.Send(&simulationv1.StreamRunEventsResponse{Event: &simulationv1.RunEvent{
					AtUnixMs: time.Now().UTC().UnixMilli(),
					RunId:    req.RunId,
					Event: &simulationv1.RunEvent_MetricsSnapshot{
						MetricsSnapshot: &simulationv1.MetricsSnapshot{Metrics: rec.Metrics},
					},
				}}); err != nil {
					return err
				}
			}

			// Exit when terminal status is reached
			if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED ||
				rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_FAILED ||
				rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_CANCELLED {
				return nil
			}
		}
	}
}
