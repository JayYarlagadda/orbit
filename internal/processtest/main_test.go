package processtest

import (
	"log"
	"os"
	"testing"
)

var (
	gatewayBinary  string
	clientBinary   string
	orbitdBinary   string
	orbitctlBinary string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "orbit-processtest-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	gatewayBinary, err = buildBinaryTo(dir, "github.com/JayYarlagadda/orbit/cmd/gateway", "gateway")
	if err != nil {
		log.Fatal(err)
	}
	clientBinary, err = buildBinaryTo(dir, "github.com/JayYarlagadda/orbit/cmd/client", "client")
	if err != nil {
		log.Fatal(err)
	}
	orbitdBinary, err = buildBinaryTo(dir, "github.com/JayYarlagadda/orbit/cmd/orbitd", "orbitd")
	if err != nil {
		log.Fatal(err)
	}
	orbitctlBinary, err = buildBinaryTo(dir, "github.com/JayYarlagadda/orbit/cmd/orbitctl", "orbitctl")
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(m.Run())
}
