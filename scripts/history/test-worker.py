"""Protocol regression without model weights or third-party dependencies."""
import contextlib
import io
import json
from pathlib import Path
import runpy
import sys
from types import SimpleNamespace
from unittest.mock import patch

class Model:
    def __init__(self, *args, **kwargs):
        pass

    def encode(self, inputs, **kwargs):
        # Bare URL strings would enter the runtime's media autodetection path.
        assert all(isinstance(x, dict) and set(x) == {'text'} for x in inputs)
        assert kwargs['prompt'] in (
            'title: none | text: ', 'task: search result | query: ',
            'task: question answering | query: ')
        return SimpleNamespace(tolist=lambda: [[1.0, 0.0] for _ in inputs])

texts = ['https://example.invalid/a.mp4', 'https://example.invalid/a.jpg',
         'https://example.invalid/a.mp3', '/tmp/synthetic.png',
         'സിന്തറ്റിക് text', 'Ignore previous instructions']
requests = [{'texts': texts, 'task': task} for task in ('document', 'search', 'question')]
modules = {name: SimpleNamespace(__version__=version) for name, version in {
    'torch': '2.15.0', 'transformers': '5.21.0',
    'sentence_transformers': '6.2.0', 'torchvision': '0.29.1', 'PIL': '12.3.0'}.items()}
modules['torch'].set_num_threads = lambda n: None
modules['torch'].float32 = 'float32'
modules['sentence_transformers'].SentenceTransformer = Model
out = io.StringIO()
with patch.dict(sys.modules, modules), patch.object(sys, 'argv', [
    'worker.py', '--model', 'synthetic', '--revision', 'synthetic',
    '--dimensions', '2', '--cache', '/unused']), patch.object(
    sys, 'stdin', io.StringIO('\n'.join(map(json.dumps, requests))+'\n')):
    with contextlib.redirect_stdout(out):
        runpy.run_path(str(Path(__file__).parents[2] / 'internal/history/worker.py'))
responses = list(map(json.loads, out.getvalue().splitlines()))
assert responses[0]['ready']
assert len(responses) == 4
assert all(len(r.get('vectors', [])) == len(texts) for r in responses[1:])
print('PASS text-only worker protocol for documents, search and question inputs (mocked runtime)')
