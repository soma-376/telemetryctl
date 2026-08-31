package codexapp

import (
	"encoding/json"
	"fmt"
)

type request struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type notification struct {
	Method string `json:"method"`
}

type response struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type initializeParams struct {
	ClientInfo   clientInfo     `json:"clientInfo"`
	Capabilities map[string]any `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func decodeResponse(line []byte) (response, error) {
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil || resp.ID == nil {
		return response{}, fmt.Errorf("%w: 응답 envelope를 읽지 못함", ErrProtocol)
	}
	if len(resp.Error) != 0 && string(resp.Error) != "null" {
		return response{}, fmt.Errorf("%w: 요청이 거부됨", ErrProtocol)
	}
	return resp, nil
}
