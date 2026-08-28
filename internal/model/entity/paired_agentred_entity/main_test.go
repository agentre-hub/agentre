package paired_agentred_entity

import (
	"os"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"

	_ "github.com/agentre-hub/agentre/internal/pkg/code" // register i18n maps
)

func TestMain(m *testing.M) {
	// Tests assert English error messages; set the default lang accordingly.
	i18n.DefaultLang = "en"
	os.Exit(m.Run())
}
