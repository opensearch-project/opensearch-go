## Data Streams API

### Create Data Streams

- Create new client

```
client, err := opensearch.NewDefaultClient()
if err != nil {
    panic(err)
}
```

- Create template index

```
iPut := opensearchapi.IndicesPutIndexTemplateRequest{
	Name:       "demo-data-template",
	Pretty:     true,
	Human:      true,
	ErrorTrace: true,
	Body:       strings.NewReader(`{"index_patterns": ["demo-*"], "data_stream": {}, "priority": 100} }`),
}
iPutResponse, err := iPut.Do(context.Background(), client)
```

- Prepare request object

```
es := opensearchapi.IndicesCreateDataStreamRequest{
	Name:       "demo-name",
	Human:      true,
	Pretty:     true,
	ErrorTrace: true,
	Header: map[string][]string{
		"Content-Type": {"application/json"},
	},
}
```

- Execute request

```
res, err := es.Do(context.TODO(), client)
if err != nil {
	// do not panic in production code
	panic(err)
}
```

- Try to read response

```
defer res.Body.Close()
body, err := ioutil.ReadAll(res.Body)
if err != nil {
	// do not panic in production code
	panic(err)
}

fmt.Println("Response Status Code: ", res.StatusCode)
fmt.Println("Response Headers: ", res.Header)
fmt.Println("Response Body: ", string(body))
```

- Successfully created data stream

```
Response Status Code: 200
Response Headers:     map[Content-Length:[28] Content-Type:[application/json; charset=UTF-8]]
Response Body:        {"acknowledged" : true}
```

### Delete Data Streams

- Create new client as previous example
- Prepare request object

```
opensearchapi.IndicesDeleteDataStreamRequest{
	Name:       "demo-name",
	Pretty:     true,
	Human:      true,
	ErrorTrace: true,
	Header: map[string][]string{
		"Content-Type": {"application/json"},
	},
}
```

- Execute request as previous example
- Try to read response as previous example
- Successfully deleted data stream

```
Response Status Code: 200
Response Headers:     map[Content-Length:[28] Content-Type:[application/json; charset=UTF-8]]
Response Body:        {"acknowledged" : true}
```

### Get All Data Streams

- Create new client as previous example
- Prepare request object

```
r := opensearchapi.IndicesGetDataStreamRequest{
	Pretty:     true,
	Human:      true,
	ErrorTrace: true,
	Header: map[string][]string{
		"Content-Type": {"application/json"},
	},
}
```

- Execute request as previous example
- Try to read response as previous example
- Successfully retrieved data streams

```
Response Status Code: 200
Response Headers:     map[Content-Length:[28] Content-Type:[application/json; charset=UTF-8]]
Response Body: 	      {"data_streams":[{"name":"demo-name","timestamp_field":{"name":"@timestamp"},"indices":[{"index_name":".ds-demo-2023-03-21-23-33-46-000001","index_uuid":"NnzgqnP0ThS7LOMHJuZ-VQ"}],"generation":1,"status":"YELLOW","template":"demo-data-template"}]}
```

### Get Specific Data Stream

- Create new client as previous example
- Prepare request object

```
r := opensearchapi.IndicesGetDataStreamRequest{
		Name: 	 	"demo-name",
		Pretty:     true,
		Human:      true,
		ErrorTrace: true,
		Header: map[string][]string{
			"Content-Type": {"application/json"},
		},
	}
```

- Execute request as previous example
- Try to read response as previous example
- Successfully retrieved data stream

```
Response Status Code: 200
Response Headers:     map[Content-Length:[28] Content-Type:[application/json; charset=UTF-8]]
Response Body:        {"data_streams":[{"name":"demo-name","timestamp_field":{"name":"@timestamp"},"indices":[{"index_name":".ds-demo-2023-03-21-23-31-50-000001","index_uuid":"vhsowqdeRFCmr1GgQ7mIsQ"}],"generation":1,"status":"YELLOW","template":"demo-data-template"}]}
```

### Get Specific Data Stream Stats

- Create new client as as previous example
- Prepare request object

```
r := opensearchapi.IndicesGetDataStreamStatsRequest{
	Name:       "demo-name",
	Pretty:     true,
	Human:      true,
	ErrorTrace: true,
	Header: map[string][]string{
		"Content-Type": {"application/json"},
	},
}
```

- Execute request as previous example
- Try to read response as previous example
- Successfully retrieved data stream stats

```
Response Status Code: 200
Response Headers:     map[Content-Length:[28] Content-Type:[application/json; charset=UTF-8]]
Response Body:        {"_shards":{"total":2,"successful":1,"failed":0},"data_stream_count":1,"backing_indices":1,"total_store_size":"208b","total_store_size_bytes":208,"data_streams":[{"data_stream":"demo-name","backing_indices":1,"store_size":"208b","store_size_bytes":208,"maximum_timestamp":0}]}
```
