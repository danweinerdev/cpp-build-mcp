package builder

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

// ---------------------------------------------------------------------------
// Unit tests — no make binary required
// ---------------------------------------------------------------------------

func TestMakeBuilderBuildArgs(t *testing.T) {
	t.Run("basic build args", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewMakeBuilder(cfg)
		args := b.buildBuildArgs(nil, 0)

		assertContainsSequence(t, args, "-C", "build")
		// No -j flag when jobs is 0
		for _, a := range args {
			if len(a) >= 2 && a[0] == '-' && a[1] == 'j' {
				t.Errorf("expected no -j flag, got %q", a)
			}
		}
	})

	t.Run("with targets", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewMakeBuilder(cfg)
		args := b.buildBuildArgs([]string{"app", "lib"}, 0)

		assertContains(t, args, "app")
		assertContains(t, args, "lib")
	})

	t.Run("with jobs", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewMakeBuilder(cfg)
		args := b.buildBuildArgs(nil, 4)

		assertContains(t, args, "-j4")
	})

	t.Run("diagnostic serial build forces j1", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:              "build",
			BuildTimeout:          5 * time.Minute,
			DiagnosticSerialBuild: true,
		}
		b := NewMakeBuilder(cfg)
		args := b.buildBuildArgs(nil, 8)

		assertContains(t, args, "-j1")
		assertNotContains(t, args, "-j8")
		assertContains(t, args, "-k")
	})

	t.Run("targets and jobs together", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "out",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewMakeBuilder(cfg)
		args := b.buildBuildArgs([]string{"all"}, 2)

		assertContainsSequence(t, args, "-C", "out")
		assertContains(t, args, "all")
		assertContains(t, args, "-j2")
	})
}

func TestMakeBuilderBuildArgsWithDirty(t *testing.T) {
	t.Run("dirty flag triggers clean args", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewMakeBuilder(cfg)
		b.SetDirty(true)

		// Verify clean args are correct (these would be used for the
		// clean step before the build).
		cleanArgs := b.buildCleanArgs()
		assertContainsSequence(t, cleanArgs, "-C", "build")
		assertContains(t, cleanArgs, "clean")
	})

	t.Run("SetDirty sets and clears flag", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewMakeBuilder(cfg)

		if b.dirty {
			t.Fatal("expected dirty to be false initially")
		}

		b.SetDirty(true)
		if !b.dirty {
			t.Fatal("expected dirty to be true after SetDirty(true)")
		}

		b.SetDirty(false)
		if b.dirty {
			t.Fatal("expected dirty to be false after SetDirty(false)")
		}
	})
}

func TestMakeBuilderEnvInjection(t *testing.T) {
	t.Run("gcc toolchain injects json flag", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:              "build",
			BuildTimeout:          5 * time.Minute,
			Toolchain:             "gcc",
			InjectDiagnosticFlags: true,
		}
		b := NewMakeBuilder(cfg)

		os.Unsetenv("CFLAGS")
		os.Unsetenv("CXXFLAGS")

		env := b.buildEnv()

		assertEnvContains(t, env, "CFLAGS", "-fdiagnostics-format=json")
		assertEnvContains(t, env, "CXXFLAGS", "-fdiagnostics-format=json")
	})

	t.Run("clang toolchain injects sarif flag", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:              "build",
			BuildTimeout:          5 * time.Minute,
			Toolchain:             "clang",
			InjectDiagnosticFlags: true,
		}
		b := NewMakeBuilder(cfg)

		os.Unsetenv("CFLAGS")
		os.Unsetenv("CXXFLAGS")

		env := b.buildEnv()

		assertEnvContains(t, env, "CFLAGS", "-fdiagnostics-format=sarif -Wno-sarif-format-unstable")
		assertEnvContains(t, env, "CXXFLAGS", "-fdiagnostics-format=sarif -Wno-sarif-format-unstable")
	})

	t.Run("empty toolchain defaults to json flag", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:              "build",
			BuildTimeout:          5 * time.Minute,
			InjectDiagnosticFlags: true,
		}
		b := NewMakeBuilder(cfg)

		os.Unsetenv("CFLAGS")
		os.Unsetenv("CXXFLAGS")

		env := b.buildEnv()

		assertEnvContains(t, env, "CFLAGS", "-fdiagnostics-format=json")
		assertEnvContains(t, env, "CXXFLAGS", "-fdiagnostics-format=json")
	})

	t.Run("appends to existing CFLAGS and CXXFLAGS", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:              "build",
			BuildTimeout:          5 * time.Minute,
			Toolchain:             "gcc",
			InjectDiagnosticFlags: true,
		}
		b := NewMakeBuilder(cfg)

		os.Setenv("CFLAGS", "-O2")
		os.Setenv("CXXFLAGS", "-std=c++17")
		defer os.Unsetenv("CFLAGS")
		defer os.Unsetenv("CXXFLAGS")

		env := b.buildEnv()

		assertEnvContains(t, env, "CFLAGS", "-O2 -fdiagnostics-format=json")
		assertEnvContains(t, env, "CXXFLAGS", "-std=c++17 -fdiagnostics-format=json")
	})
}

func TestMakeBuilderNoInjection(t *testing.T) {
	cfg := &config.Config{
		BuildDir:              "build",
		BuildTimeout:          5 * time.Minute,
		InjectDiagnosticFlags: false,
	}
	b := NewMakeBuilder(cfg)

	os.Unsetenv("CFLAGS")
	os.Unsetenv("CXXFLAGS")

	env := b.buildEnv()

	// Verify CFLAGS and CXXFLAGS are not set when injection is disabled.
	for _, e := range env {
		if len(e) >= 7 && e[:7] == "CFLAGS=" {
			t.Errorf("expected no CFLAGS in env, got %q", e)
		}
		if len(e) >= 9 && e[:9] == "CXXFLAGS=" {
			t.Errorf("expected no CXXFLAGS in env, got %q", e)
		}
	}
}

func TestMakeBuilderConfigureIsNoop(t *testing.T) {
	cfg := &config.Config{
		BuildDir:     "build",
		BuildTimeout: 5 * time.Minute,
	}
	b := NewMakeBuilder(cfg)

	result, err := b.Configure(context.Background(), []string{"--some-arg"})
	if err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected ExitCode 0, got %d", result.ExitCode)
	}
	if result.Stdout != "" {
		t.Fatalf("expected empty Stdout, got %q", result.Stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("expected empty Stderr, got %q", result.Stderr)
	}
}

func TestNewBuilderMake(t *testing.T) {
	cfg := &config.Config{Generator: "make"}
	b, err := NewBuilder(cfg)
	if err != nil {
		t.Fatalf("NewBuilder() returned error: %v", err)
	}
	mb, ok := b.(*MakeBuilder)
	if !ok {
		t.Fatalf("expected *MakeBuilder, got %T", b)
	}
	if mb.cfg != cfg {
		t.Fatal("expected MakeBuilder to hold the same config pointer")
	}
}

func TestMakeBuilderListTargetsNotSupported(t *testing.T) {
	cfg := &config.Config{BuildDir: "build", BuildTimeout: 5 * time.Minute}
	b := NewMakeBuilder(cfg)

	_, err := b.ListTargets(context.Background())
	if err == nil {
		t.Fatal("expected error from ListTargets")
	}
	if !errors.Is(err, ErrTargetsNotSupported) {
		t.Fatalf("expected ErrTargetsNotSupported, got %v", err)
	}
}

func TestMakeBuilderCleanArgs(t *testing.T) {
	cfg := &config.Config{BuildDir: "output"}
	b := NewMakeBuilder(cfg)
	args := b.buildCleanArgs()

	assertContainsSequence(t, args, "-C", "output")
	assertContains(t, args, "clean")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// assertEnvContains checks that the env slice contains key=expectedValue.
func assertEnvContains(t *testing.T, env []string, key, expectedValue string) {
	t.Helper()
	prefix := key + "="
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			got := e[len(prefix):]
			if got != expectedValue {
				t.Errorf("expected %s=%q, got %s=%q", key, expectedValue, key, got)
			}
			return
		}
	}
	t.Errorf("expected env to contain %s, but it was not found", key)
}
