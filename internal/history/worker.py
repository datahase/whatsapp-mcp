"""Local text-only worker. Protocol output contains no logs or telemetry."""
import argparse, contextlib, json, os, sys
os.environ.update(HF_HUB_DISABLE_TELEMETRY="1", HF_HUB_DISABLE_IMPLICIT_TOKEN="1", DO_NOT_TRACK="1", TOKENIZERS_PARALLELISM="false", WANDB_DISABLED="true")
p=argparse.ArgumentParser()
p.add_argument('--model',required=True);p.add_argument('--revision',required=True)
p.add_argument('--dimensions',type=int,required=True);p.add_argument('--device',default='cpu');p.add_argument('--cache',required=True);p.add_argument('--offline',action='store_true')
a=p.parse_args()
try:
 with contextlib.redirect_stdout(sys.stderr):
  import torch, transformers, sentence_transformers, torchvision, PIL
  from sentence_transformers import SentenceTransformer
  torch.set_num_threads(2)
  dtype=torch.bfloat16 if a.device.startswith('cuda') and torch.cuda.is_bf16_supported() else torch.float32
  model=SentenceTransformer(a.model,revision=a.revision,device=a.device,cache_folder=a.cache,local_files_only=a.offline,trust_remote_code=False,config_kwargs={'vision_config':None,'audio_config':None},model_kwargs={'dtype':dtype})
  model.max_seq_length=2048
 print(json.dumps({'ready':True,'runtime':{'sentence_transformers':sentence_transformers.__version__,'transformers':transformers.__version__,'torch':torch.__version__.split('+')[0], 'pillow':PIL.__version__, 'torchvision':torchvision.__version__.split('+')[0]},'dimensions':a.dimensions,'revision':a.revision,'device':a.device}),flush=True)
except Exception:
 print(json.dumps({'error':'Local model could not load. Install pinned history requirements and download the configured revision.'}),flush=True);sys.exit(1)
prompts={'document':'title: none | text: ','search':'task: search result | query: ','question':'task: question answering | query: '}
for line in sys.stdin:
 try:
  if len(line)>4*1024*1024:raise ValueError()
  r=json.loads(line);texts=r['texts'];task=r['task']
  if not isinstance(texts,list) or not 0<len(texts)<=16 or any(not isinstance(t,str) or len(t)>10000 for t in texts):raise ValueError()
  with contextlib.redirect_stdout(sys.stderr):
   # Explicit text prevents URL/path auto-detection from fetching or decoding media.
   result=model.encode([{'text':text} for text in texts],prompt=prompts[task],batch_size=8,truncate_dim=a.dimensions,normalize_embeddings=True,show_progress_bar=False,convert_to_numpy=True)
  print(json.dumps({'vectors':result.tolist()},allow_nan=False),flush=True)
 except Exception:
  print(json.dumps({'error':'Local embedding failed. Input was not logged.'}),flush=True)
