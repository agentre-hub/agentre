package rpcerror_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

func TestError_GivenWrappedStableError_WhenClassified_ThenPreservesCodeMessageAndDetails(t *testing.T) {
	want := &rpcerror.Error{Code: rpcerror.CodeMethodNotFound, Message: "method not found", Details: []byte{1, 2}}
	err := fmt.Errorf("call failed: %w", want)

	var got *rpcerror.Error
	require.True(t, errors.As(err, &got))
	require.Same(t, want, got)
	require.Equal(t, "method not found", got.Error())
}

func TestError_GivenStandardCodes_WhenUsedAcrossTransports_ThenValuesStayStable(t *testing.T) {
	require.Equal(t, int32(-32601), rpcerror.CodeMethodNotFound)
	require.Equal(t, int32(-32602), rpcerror.CodeInvalidParams)
	require.Equal(t, int32(-32603), rpcerror.CodeInternal)
	require.Equal(t, int32(-32800), rpcerror.CodeCanceled)
}

// Given a personal token that lacks the scope a method needs, When the refusal is
// built, Then it carries its own stable code (not -32001, which would make the
// caller refresh a credential that was never rejected) and names the missing scope.
func TestErrScopeDenied_GivenAMissingScope_ThenCarriesItsOwnCodeAndNamesTheScope(t *testing.T) {
	require.Equal(t, int32(-32005), rpcerror.CodeScopeDenied)

	err := rpcerror.ErrScopeDenied("chat")

	require.Equal(t, rpcerror.CodeScopeDenied, err.Code)
	require.Equal(t, "token lacks scope chat", err.Error())
	require.NotEqual(t, rpcerror.CodeUnauthorized, err.Code)
}
