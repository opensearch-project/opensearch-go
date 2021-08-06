#!/bin/bash
set -ex

/usr/share/elasticsearch/bin/elasticsearch -Ediscovery.type=single-node
