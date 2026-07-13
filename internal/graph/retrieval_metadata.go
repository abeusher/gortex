package graph

const (
	metaRetrievalSignature = "search_signature"
	metaRetrievalQualName  = "search_qual_name"
	metaRetrievalDoc       = "search_doc"
)

// RetrievalMetadata is the search/display projection of parser-owned symbol
// metadata. It is deliberately separate from Node.QualName and the parser's
// signature/doc values so retrieval heuristics cannot alter resolver identity.
type RetrievalMetadata struct {
	Signature string
	QualName  string
	Doc       string
}

// RetrievalMetadata returns normalized retrieval fields, falling back to the
// parser-owned values for graph snapshots created before normalization.
// QualName is empty unless the parser or normalizer had semantic owner evidence.
func (n *Node) RetrievalMetadata() RetrievalMetadata {
	if n == nil {
		return RetrievalMetadata{}
	}
	qualName := firstMetadataString(n.Meta, metaRetrievalQualName, "")
	if qualName == "" {
		qualName = n.QualName
	}
	return RetrievalMetadata{
		Signature: firstMetadataString(n.Meta, metaRetrievalSignature, "signature"),
		QualName:  qualName,
		Doc:       firstMetadataString(n.Meta, metaRetrievalDoc, "doc"),
	}
}

// SetRetrievalMetadata replaces the retrieval-only projection. Empty values
// remove prior projections; parser-owned metadata and Node.QualName are never
// modified.
func SetRetrievalMetadata(n *Node, metadata RetrievalMetadata) {
	if n == nil {
		return
	}
	if n.Meta == nil {
		if metadata.Signature == "" && metadata.QualName == "" && metadata.Doc == "" {
			return
		}
		n.Meta = make(map[string]any)
	}
	setMetadataString(n.Meta, metaRetrievalSignature, metadata.Signature)
	setMetadataString(n.Meta, metaRetrievalQualName, metadata.QualName)
	setMetadataString(n.Meta, metaRetrievalDoc, metadata.Doc)
}

func firstMetadataString(meta map[string]any, primary, fallback string) string {
	if value, _ := meta[primary].(string); value != "" {
		return value
	}
	if fallback != "" {
		value, _ := meta[fallback].(string)
		return value
	}
	return ""
}

func setMetadataString(meta map[string]any, key, value string) {
	if value == "" {
		delete(meta, key)
		return
	}
	meta[key] = value
}
