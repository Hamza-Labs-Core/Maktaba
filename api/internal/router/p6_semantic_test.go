package router

import (
	"context"
	"testing"

	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/search"
)

// plainPipeline is a pipeline client without the SemanticSearch RPC.
type plainPipeline struct{ grpcpipeline.Client }

// semanticPipeline adds the RPC.
type semanticPipeline struct{ grpcpipeline.Client }

func (semanticPipeline) SemanticSearch(context.Context, string, int, []string) ([]grpcpipeline.SemanticHit, error) {
	return nil, nil
}

type explicitSemantic struct{}

func (explicitSemantic) Search(context.Context, string, int, search.Filters) ([]search.Hit, error) {
	return nil, nil
}

func TestSearchSemantic_Wiring(t *testing.T) {
	if got := searchSemantic(P6Deps{}); got != nil {
		t.Fatalf("no pipeline client: want nil (FTS-only), got %T", got)
	}
	if got := searchSemantic(P6Deps{PipelineClient: plainPipeline{}}); got != nil {
		t.Fatalf("client without SemanticSearch: want nil, got %T", got)
	}
	got := searchSemantic(P6Deps{PipelineClient: semanticPipeline{}})
	if _, ok := got.(*search.GRPCSemantic); !ok {
		t.Fatalf("client with SemanticSearch: want *search.GRPCSemantic, got %T", got)
	}
	explicit := explicitSemantic{}
	if got := searchSemantic(P6Deps{PipelineClient: semanticPipeline{}, SearchSemantic: explicit}); got != explicit {
		t.Fatalf("an explicit SearchSemantic wins, got %T", got)
	}
	// The production client (what main.go builds from MAKTABA_PIPELINE_ADDR)
	// carries the RPC, so setting the address enables semantic search.
	cfg := grpcpipeline.DefaultConfig()
	cfg.Addr = "127.0.0.1:1"
	if _, ok := searchSemantic(P6Deps{PipelineClient: grpcpipeline.NewRealClient(cfg)}).(*search.GRPCSemantic); !ok {
		t.Fatal("the real pipeline client must enable semantic search")
	}
}
