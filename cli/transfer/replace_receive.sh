#!/bin/bash

# Replace receive.go with the fixed version
rm -f receive.go
mv receive_fixed.go receive.go

# List the files in the directory
ls -la