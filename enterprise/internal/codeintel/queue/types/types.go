package types

// TODO - share some of these?
type DequeueRequest struct {
	IndexerName string `json:"indexerName"`
}

type CompleteRequest struct {
	IndexerName  string `json:"indexerName"`
	IndexID      int    `json:"indexId"`
	ErrorMessage string `json:"errorMessage"`
}

type HeartbeatRequest struct {
	IndexerName string `json:"indexerName"`
}
