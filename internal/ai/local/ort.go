package local

import (
	"errors"
	"fmt"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// initOnce guards the global ONNX runtime initialization.
var initOnce sync.Once
var initErr error

// runtimeDylibPath returns the user-level path where libonnxruntime.dylib
// is expected. The first-run wizard / install path drops it here, so we
// point onnxruntime_go straight at it instead of relying on DYLD search.
func runtimeDylibPath() string { return appdir.RuntimeDylib() }

func ensureRuntime() error {
	initOnce.Do(func() {
		// Point onnxruntime_go at the user-level dylib if present. When
		// it isn't, fall back to whatever the DYLD loader can find — the
		// downstream session creation will return a clear error if no
		// dylib is reachable, and the analyzer skips ML-backed signals
		// without failing the analyze step.
		if p := runtimeDylibPath(); p != "" {
			if _, statErr := os.Stat(p); statErr == nil {
				ort.SetSharedLibraryPath(p)
			}
		}
		if err := ort.InitializeEnvironment(); err != nil {
			initErr = fmt.Errorf("init onnxruntime: %w", err)
		}
	})
	return initErr
}

// OrtSession wraps a single-input single-output ONNX model session.
// We use DynamicAdvancedSession so that input/output tensors can be passed
// at Run time rather than being fixed at construction.
type OrtSession struct {
	sess        *ort.DynamicAdvancedSession
	inputName   string
	outputName  string
	outputShape ort.Shape
	closed      bool
}

// NewSession loads the model at modelPath and prepares it for inference.
// CoreML EP is requested when available; falls back to CPU.
func NewSession(modelPath string) (*OrtSession, error) {
	if err := ensureRuntime(); err != nil {
		return nil, err
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("new session options: %w", err)
	}
	defer func() { _ = opts.Destroy() }()

	// Best-effort CoreML EP append; ignore the error (older runtimes / non-mac builds).
	_ = opts.AppendExecutionProviderCoreML(0)

	inputs, outputs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("inspect model: %w", err)
	}
	if len(inputs) == 0 || len(outputs) == 0 {
		return nil, errors.New("model has no inputs or outputs")
	}

	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{inputs[0].Name}, []string{outputs[0].Name},
		opts)
	if err != nil {
		return nil, fmt.Errorf("new dynamic advanced session: %w", err)
	}

	return &OrtSession{
		sess:        sess,
		inputName:   inputs[0].Name,
		outputName:  outputs[0].Name,
		outputShape: outputs[0].Dimensions,
	}, nil
}

// Run executes a single forward pass with input shaped as `shape`.
// Caller is responsible for presenting input in the model's expected layout
// (e.g. NCHW float32). The returned slice is copied; caller may retain it.
func (s *OrtSession) Run(input []float32, shape []int64) ([]float32, error) {
	if s.closed {
		return nil, errors.New("session closed")
	}
	in, err := ort.NewTensor(ort.NewShape(shape...), input)
	if err != nil {
		return nil, fmt.Errorf("new input tensor: %w", err)
	}
	defer func() { _ = in.Destroy() }()

	// Replace any dynamic dims (-1) the model reports with concrete
	// values so NewEmptyTensor doesn't bail with "Invalid tensor shape".
	// We assume batch=1 here (matches every signal's preprocess pipeline).
	concrete := make([]int64, len(s.outputShape))
	outFlat := int64(1)
	for i, d := range s.outputShape {
		if d <= 0 {
			concrete[i] = 1
		} else {
			concrete[i] = d
		}
		outFlat *= concrete[i]
	}
	outShape := ort.NewShape(concrete...)
	if len(concrete) == 0 {
		outShape = ort.NewShape(outFlat)
	}

	out, err := ort.NewEmptyTensor[float32](outShape)
	if err != nil {
		return nil, fmt.Errorf("new output tensor: %w", err)
	}
	defer func() { _ = out.Destroy() }()

	if err := s.sess.Run([]ort.Value{in}, []ort.Value{out}); err != nil {
		return nil, fmt.Errorf("run session: %w", err)
	}

	src := out.GetData()
	cp := make([]float32, len(src))
	copy(cp, src)
	return cp, nil
}

// Close releases the session.
func (s *OrtSession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.sess.Destroy()
}
