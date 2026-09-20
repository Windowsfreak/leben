import os
import time
from typing import List, Union
import numpy as np
import onnxruntime as ort
from fastapi import FastAPI, HTTPException, Request, Response
from pydantic import BaseModel
from tokenizers import Tokenizer
from huggingface_hub import hf_hub_download

MODEL_REPO = os.environ.get("MODEL_REPO", "onnx-community/granite-embedding-97m-multilingual-r2-ONNX")
MODEL_FILE = os.environ.get("MODEL_FILE", "onnx/model.onnx")
MODEL_DIR = os.environ.get("MODEL_DIR", os.path.join(os.path.dirname(__file__), "models"))
DIMENSION = 384

from contextlib import asynccontextmanager


@asynccontextmanager
async def lifespan(app: FastAPI):
    if session is None or tokenizer is None:
        load_artifacts()
    yield


app = FastAPI(title="Leben ONNX Embedding Sidecar", version="1.0.0", lifespan=lifespan)

tokenizer: Tokenizer = None
session: ort.InferenceSession = None


def load_artifacts():
    global tokenizer, session
    if session is not None and tokenizer is not None:
        return
    os.makedirs(MODEL_DIR, exist_ok=True)
    
    # 1. Resolve Tokenizer
    local_tok = os.path.join(MODEL_DIR, "tokenizer.json")
    if not os.path.exists(local_tok):
        print(f"Downloading tokenizer from {MODEL_REPO}...")
        downloaded = hf_hub_download(MODEL_REPO, "tokenizer.json", local_dir=MODEL_DIR)
        local_tok = downloaded
    tokenizer = Tokenizer.from_file(local_tok)
    tokenizer.enable_truncation(max_length=512)
    tokenizer.enable_padding(pad_id=0, pad_token="<pad>")

    # 2. Resolve ONNX Model
    local_onnx = os.path.join(MODEL_DIR, os.path.basename(MODEL_FILE))
    if not os.path.exists(local_onnx):
        alt_onnx = os.path.join(MODEL_DIR, MODEL_FILE)
        if os.path.exists(alt_onnx):
            local_onnx = alt_onnx
        else:
            print(f"Downloading ONNX model ({MODEL_FILE}) from {MODEL_REPO}...")
            downloaded = hf_hub_download(MODEL_REPO, MODEL_FILE, local_dir=MODEL_DIR)
            local_onnx = downloaded

    print(f"Loading ONNX runtime session from {local_onnx}...")
    sess_options = ort.SessionOptions()
    sess_options.enable_cpu_mem_arena = False
    sess_options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
    sess_options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
    threads = int(os.environ.get("ORT_NUM_THREADS", "2"))
    sess_options.intra_op_num_threads = threads
    sess_options.inter_op_num_threads = 1

    session = ort.InferenceSession(local_onnx, sess_options)
    print(f"ONNX session loaded successfully (dim={DIMENSION}).")


class EmbedRequest(BaseModel):
    # Support single string or list of strings
    input: Union[str, List[str]]
    # Optional model parameter for backward compatibility
    model: str = ""


@app.get("/health")
def health():
    return {
        "status": "ok",
        "model": MODEL_REPO,
        "dim": DIMENSION,
    }


@app.post("/api/embed")
async def embed(req: EmbedRequest, request: Request):
    if session is None or tokenizer is None:
        load_artifacts()

    is_batch = isinstance(req.input, list)
    texts = req.input if is_batch else [req.input]

    if len(texts) == 0:
        if is_batch:
            return {"embeddings": [], "dim": DIMENSION}
        return {"embedding": [], "dim": DIMENSION}

    # 1. Tokenize batch with dynamic padding & truncation
    encodings = tokenizer.encode_batch(texts)
    input_ids = np.array([e.ids for e in encodings], dtype=np.int64)
    attention_mask = np.array([e.attention_mask for e in encodings], dtype=np.int64)

    # 2. Run ONNX forward pass
    outputs = session.run(["last_hidden_state"], {
        "input_ids": input_ids,
        "attention_mask": attention_mask
    })

    # 3. CLS token pooling (Granite R2 architecture uses token 0)
    cls_vectors = outputs[0][:, 0, :]

    # 4. L2 normalization for exact cosine similarity
    norms = np.linalg.norm(cls_vectors, axis=-1, keepdims=True)
    normalized = (cls_vectors / np.clip(norms, a_min=1e-9, a_max=None)).astype(np.float32)

    # 5. Check Content Negotiation: Binary vs JSON
    accept_header = request.headers.get("accept", "")
    if "application/octet-stream" in accept_header:
        # Stream raw little-endian float32 bytes directly
        raw_bytes = normalized.tobytes()
        return Response(
            content=raw_bytes,
            media_type="application/octet-stream",
            headers={
                "X-Batch-Size": str(len(texts)),
                "X-Embedding-Dim": str(DIMENSION),
            }
        )

    # Default: Return JSON float array
    vectors_list = normalized.tolist()
    if is_batch:
        return {
            "embeddings": vectors_list,
            "dim": DIMENSION,
        }
    else:
        return {
            "embedding": vectors_list[0],
            "embeddings": vectors_list,  # include for Ollama compatibility
            "dim": DIMENSION,
        }
