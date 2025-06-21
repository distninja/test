#!/bin/bash

# Create a rule
curl -X POST http://localhost:8080/api/rules \
  -H "Content-Type: application/json" \
  -d '{"name":"cxx","command":"g++ -c $in -o $out","description":"C++ compile rule"}'

# Get all rules
curl http://localhost:8080/api/rules

# Get specific rule
curl http://localhost:8080/api/rules/cxx
