#!/usr/bin/env python3
"""
Pre-download IBM Granite 97M ONNX model and tokenizer into ./models directory.
"""
import os
import sys
from huggingface_hub import hf_hub_download

MODEL_REPO = os.environ.get("MODEL_REPO", "onnx-community/granite-embedding-97m-multilingual-r2-ONNX")
MODEL_FILE = os.environ.get("MODEL_FILE", "onnx/model.onnx")
MODEL_DIR = os.environ.get("MODEL_DIR", os.path.join(os.path.dirname(__file__), "models"))

def main():
    os.makedirs(MODEL_DIR, exist_ok=True)
    print(f"Downloading tokenizer to {MODEL_DIR}...")
    hf_hub_download(MODEL_REPO, "tokenizer.json", local_dir=MODEL_DIR)
    print(f"Downloading ONNX model ({MODEL_FILE}) to {MODEL_DIR}...")
    hf_hub_download(MODEL_REPO, MODEL_FILE, local_dir=MODEL_DIR)
    print("Download complete. Model artifacts ready in:", MODEL_DIR)

if __name__ == "__main__":
    main()
