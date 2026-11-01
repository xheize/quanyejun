package environment

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestTransferKeyNotInherited(t *testing.T) {
	values := environmentWithOverrides([]string{
		"PATH=unchanged", "QUANYEJUN_TRANSFER_API_KEY=test-secret", "quanyejun_transfer_api_key=another-secret", "KUBECONFIG=/credentials/config",
	}, map[string]string{"HOME": "workspace-home"})
	joined := strings.Join(values, "\n")
	if strings.Contains(strings.ToUpper(joined), "QUANYEJUN_TRANSFER_API_KEY") {
		t.Fatal("management key inherited")
	}
	if !strings.Contains(joined, "KUBECONFIG=/credentials/config") || !strings.Contains(joined, "PATH=unchanged") {
		t.Fatal("unrelated tool environment removed")
	}

	t.Setenv("QUANYEJUN_TRANSFER_API_KEY", "test-only-server-key")
	root := t.TempDir()
	local := NewLocal(ConfigFromEnvironment(func(key string) string {
		if key == "QUANYEJUN_RUNTIME_ROOT" {
			return root
		}
		return ""
	}))
	if err := local.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := local.Run(context.Background(), RunSpec{Command: os.Args[0], Args: []string{"-test.run=^TestTransferKeyChild$", "--", "transfer-key-child"}})
	if err != nil || result.ExitCode != 0 || result.Stdout != "absent" {
		t.Fatalf("child key isolation failed: %+v, %v", result, err)
	}
}

func TestTransferKeyChild(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "transfer-key-child" {
		return
	}
	if _, exists := os.LookupEnv("QUANYEJUN_TRANSFER_API_KEY"); exists {
		fmt.Print("present")
	} else {
		fmt.Print("absent")
	}
	os.Exit(0)
}
