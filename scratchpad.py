#%%
%load_ext autoreload
%autoreload 2
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
