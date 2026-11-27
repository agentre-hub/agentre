package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newLLMCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm",
		Short: "Manage LLM providers",
	}
	cmd.AddCommand(newLLMListCmd(), newLLMAddCmd(), newLLMRemoveCmd())
	return cmd
}

func newLLMListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List LLM providers",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			body, err := localGET("/local/llm")
			if err != nil {
				return err
			}
			fmt.Println(string(body))
			return nil
		},
	}
}

func newLLMAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add or update an LLM provider with a stable cross-machine key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, _ := cmd.Flags().GetString("key")
			if _, err := uuid.Parse(key); err != nil {
				return newUsageError("--key must be a valid UUID (got %q)", key)
			}
			name, _ := cmd.Flags().GetString("name")
			provType, _ := cmd.Flags().GetString("type")
			baseURL, _ := cmd.Flags().GetString("base-url")
			model, _ := cmd.Flags().GetString("model")
			apiKey, _ := cmd.Flags().GetString("api-key")

			payload, _ := json.Marshal(map[string]any{
				"providerKey": key,
				"name":        name,
				"type":        provType,
				"baseURL":     baseURL,
				"model":       model,
				"apiKey":      apiKey,
			})
			resp, err := localClient().Post("http://daemon/local/llm", "application/json", bytes.NewReader(payload))
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("%s", bytes.TrimSpace(body))
			}
			fmt.Println("ok")
			return nil
		},
	}
	cmd.Flags().String("key", "", "stable provider key (UUIDv4 from desktop UI)")
	cmd.Flags().String("name", "", "human label")
	cmd.Flags().String("type", "", "anthropic|openai-chat|openai-response|gemini")
	cmd.Flags().String("base-url", "", "endpoint base URL")
	cmd.Flags().String("model", "", "default model id")
	cmd.Flags().String("api-key", "", "API key")
	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("api-key")
	return cmd
}

func newLLMRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Delete an LLM provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, _ := cmd.Flags().GetString("key")
			if _, err := uuid.Parse(key); err != nil {
				return newUsageError("--key must be a valid UUID (got %q)", key)
			}
			buf, _ := json.Marshal(map[string]string{"providerKey": key})
			req, _ := http.NewRequest(http.MethodDelete, "http://daemon/local/llm", bytes.NewReader(buf))
			resp, err := localClient().Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("%s", bytes.TrimSpace(body))
			}
			fmt.Println("ok")
			return nil
		},
	}
	cmd.Flags().String("key", "", "stable provider key (UUIDv4)")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}
