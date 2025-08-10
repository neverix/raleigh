#%%
%load_ext autoreload
%autoreload 2
#%%
from jif.jif.raleigh import RaleighInfo
import os
open(f"{os.getenv('HOME')}/.raleigh/hosts.json", "w").write("""
{"ports":[53109,51121,34501,39197,44211,35741,55853],"hosts":[["34.32.152.149",39821],["34.91.154.167",40241],["34.34.7.156",54817],["35.204.150.34",45851],["35.204.184.91",58717],["35.204.169.132",34979],null],"seed":53109,"params_seed":0,"group_id":776059}
""")
RaleighInfo.load(f"{os.getenv('HOME')}/.raleigh/hosts.json")
#%%
import jax
import jax.numpy as jnp
from jif.jif.demo import extract_dct, reconstruct_dct, ProjectConfig, project_dct
import numpy as np
#%%
mat = jax.random.normal(jax.random.key(0), (64, 128, 32))
projected = project_dct(mat, chunk_size=8)
unprojected = project_dct(projected, chunk_size=8, transpose=True)
jnp.abs(mat - unprojected).max()
#%%
from jif.jif.demo import extract_last_bulk, move_bulk_last
rebulked = extract_last_bulk(move_bulk_last(mat, 8), 8)
jnp.abs(rebulked - mat).max()
#%%
cfg = ProjectConfig(chunk_size=8, k=512)
q = extract_dct(mat, config=cfg)
recon = reconstruct_dct(q, config=cfg)
jnp.abs(mat - recon).max()
#%%
