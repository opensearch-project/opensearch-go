# Upgrading to >= 6.0.0

## Analyzer, experiment and judgment request types are discriminated by `type`

`type` is required on the `cjk` and `smartcn` analyzers and on the `search_relevance` experiment and judgment request types, so their `Type` field is `string` instead of `*string`:

- `opensearchapi.CommonAnalysisCJKAnalyzer` and `CommonAnalysisSmartcnAnalyzer`
- `opensearchapi.SearchRelevancePutPairwiseExperimentRequest`, `SearchRelevancePutPointwiseExperimentRequest` and `SearchRelevancePutHybridOptimizerExperimentRequest`
- `opensearchapi.SearchRelevancePutLLMJudgmentsRequest`, `SearchRelevancePutUBIJudgmentsRequest` and `SearchRelevancePutImportJudgmentsRequest`

Each union constructor for these types sets the field, so code that builds a value through one does not need to name it. In 5.2.0 they did not set it:

- An analyzer from `NewCommonAnalysisAnalyzerFromCJKAnalyzer` or `NewCommonAnalysisAnalyzerFromSmartcnAnalyzer` marshalled as `{}`, which OpenSearch reads as a custom analyzer and the client decodes as `CustomAnalyzer`.
- An experiment or judgment body built with a `search_relevance` constructor and no `Type` went out with no `type`, which the server rejects as an invalid or missing type. Decoding such a body picked the first branch its fields fit rather than the one its `type` named, so a pairwise or pointwise experiment decoded as a hybrid optimizer experiment. A body with no `type` now fails to decode.

The `search_relevance` experiment and judgment request types also follow the plugin's fields:

- The fields every experiment shares, `QuerySetID`, `SearchConfigurationList` and `Size`, move to an embedded `SearchRelevancePutExperimentRequestBase`. The fields every judgment shares, `Name` and `Description`, move to an embedded `SearchRelevancePutJudgmentsRequestBase`.
- Fields the plugin requires are values instead of pointers: `QuerySetID`, `Size` and `Name`, and `ModelID` and `QuerySetID` on LLM judgments, `ClickModel` and `MaxRank` on UBI judgments.
- LLM judgments gain `TokenLimit`, `PromptTemplate`, `LLMJudgmentRatingType`, `ExistingJudgments` and `OverwriteCache`. UBI judgments gain `StartDate`, `EndDate` and `UBIEventsIndex`.

Reading a moved field still compiles, since the base is embedded. Code that sets one in a composite literal, or sets `Type`, no longer compiles:

```go
// Before
a := opensearchapi.CommonAnalysisCJKAnalyzer{Type: new("cjk")}
e := opensearchapi.SearchRelevancePutPairwiseExperimentRequest{
	QuerySetID:              new("q"),
	SearchConfigurationList: []string{"a", "b"},
	Size:                    new(10),
	Type:                    new("PAIRWISE_COMPARISON"),
}

// After: the constructor sets Type.
analyzer := opensearchapi.NewCommonAnalysisAnalyzerFromCJKAnalyzer(opensearchapi.CommonAnalysisCJKAnalyzer{})
body := search_relevance.NewSearchRelevancePutExperimentsBodyFromPutPairwiseExperimentRequest(
	opensearchapi.SearchRelevancePutPairwiseExperimentRequest{
		SearchRelevancePutExperimentRequestBase: opensearchapi.SearchRelevancePutExperimentRequestBase{
			QuerySetID:              "q",
			SearchConfigurationList: []string{"a", "b"},
			Size:                    10,
		},
	},
)
```

Code that reads a field that is now a value drops the dereference:

```go
// Before
isCJK := a.Type != nil && *a.Type == "cjk"

// After
isCJK := a.Type == "cjk"
```
