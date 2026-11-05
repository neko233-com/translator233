#!/usr/bin/env python3
"""Repack an ONNX language pack into a smaller, equivalent one.

What it does (lossless, the graph and the numbers are untouched):

1. All initializers of encoder.onnx and decoder.onnx go into one shared
   weights.bin via ONNX external data. Marian ties the input embedding to the
   output projection, so both files carry an identical 65001x512 table
   (33.3MB); with a shared blob it is stored once.
2. tokenizer.json (6.4MB of pretty-printed JSON) becomes tokenizer.tok, a
   compact binary the Go loader reads directly.

Usage:
    pip install onnx numpy
    python tools/slim_pack.py .cache/pack/zh-en .cache/slim/zh-en
"""

import hashlib
import json
import os
import struct
import sys

import onnx
from onnx import numpy_helper

ALIGN = 64
MAGIC = b"T23T"
VERSION = 1


def iter_initializers(model):
    for init in model.graph.initializer:
        yield init


def tensor_bytes(init):
    """Return the raw little-endian bytes of an initializer."""
    if init.HasField("raw_data") and len(init.raw_data) > 0:
        return bytes(init.raw_data)
    arr = numpy_helper.to_array(init)
    return arr.tobytes()


def make_external(init, location, offset, length):
    init.ClearField("raw_data")
    init.data_location = onnx.TensorProto.EXTERNAL
    del init.external_data[:]
    for k, v in (("location", location), ("offset", str(offset)), ("length", str(length))):
        item = init.external_data.add()
        item.key = k
        item.value = v


def slim(models, outdir):
    blob = bytearray()
    offsets = {}  # sha256 -> (offset, length)
    order = []  # (offset, bytes) to write once

    def place(data):
        digest = hashlib.sha256(data).hexdigest()
        if digest in offsets:
            return offsets[digest], True
        while len(blob) % ALIGN:
            blob.append(0)
        off = len(blob)
        blob.extend(data)
        offsets[digest] = (off, len(data))
        order.append(off)
        return offsets[digest], False

    total_raw = 0
    for model in models:
        for init in iter_initializers(model):
            data = tensor_bytes(init)
            total_raw += len(data)
            if not data:
                continue
            (off, length), _ = place(data)
            make_external(init, "weights.bin", off, length)

    with open(os.path.join(outdir, "weights.bin"), "wb") as f:
        f.write(blob)
    return total_raw, len(blob)


def write_tokenizer(src, dst):
    with open(src, "r", encoding="utf-8") as f:
        tj = json.load(f)
    model = tj["model"]
    if model.get("type") != "Unigram":
        raise SystemExit(f"unsupported tokenizer type {model.get('type')}")
    vocab = model["vocab"]
    unk = int(model.get("unk_id", 0))

    buf = bytearray()
    buf += MAGIC
    buf += struct.pack(">III", VERSION, unk, len(vocab))
    for piece, score in vocab:
        raw = piece.encode("utf-8")
        if len(raw) > 0xFFFF:
            raise SystemExit("token too long")
        buf += struct.pack(">H", len(raw))
        buf += raw
        buf += struct.pack(">f", float(score))
    with open(dst, "wb") as f:
        f.write(buf)
    return len(buf)


def main():
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(1)
    src, dst = sys.argv[1], sys.argv[2]
    os.makedirs(dst, exist_ok=True)

    names = ["encoder.onnx", "decoder.onnx"]
    models = [onnx.load(os.path.join(src, n)) for n in names]

    before = sum(os.path.getsize(os.path.join(src, n)) for n in names)
    raw, unique = slim(models, dst)

    for name, model in zip(names, models):
        onnx.save_model(model, os.path.join(dst, name))

    tok_src = os.path.join(src, "tokenizer.json")
    tok_size = os.path.getsize(tok_src)
    tok_new = write_tokenizer(tok_src, os.path.join(dst, "tokenizer.tok"))

    # config.json is tiny and goes as-is.
    with open(os.path.join(src, "config.json"), "rb") as f:
        cfg = f.read()
    with open(os.path.join(dst, "config.json"), "wb") as f:
        f.write(cfg)

    after = sum(
        os.path.getsize(os.path.join(dst, n))
        for n in ["encoder.onnx", "decoder.onnx", "weights.bin", "tokenizer.tok", "config.json"]
    )
    print(f"initializer bytes: {raw/1e6:.1f}MB, unique: {unique/1e6:.1f}MB")
    print(f"tokenizer: {tok_size/1e6:.2f}MB -> {tok_new/1e6:.2f}MB")
    print(f"pack: {(before + tok_size)/1e6:.1f}MB -> {after/1e6:.1f}MB (-{100*(1-after/(before+tok_size)):.0f}%)")


if __name__ == "__main__":
    main()
