// /ficha/:id lleva a la ficha que corresponde según el tipo (película, o la serie en la temporada de un episodio). Lo
// usa el reproductor al salir cuando no hay historial al que volver (enlace directo a /ver/…).
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { detailHref, type ItemDetail } from '../lib/catalog';
import { Banner, Loading } from '../components/ui';

export function ItemRedirect() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    api<{ item: ItemDetail }>('GET', `/api/v1/items/${id}`)
      .then(({ item }) => navigate(detailHref(item), { replace: true }))
      .catch((e) => setError(e instanceof ApiError ? e.message : 'No se ha podido abrir la ficha.'));
  }, [id, navigate]);
  if (error)
    return (
      <main className="mx-auto w-full max-w-content p-6">
        <Banner tone="error">{error}</Banner>
      </main>
    );
  return <Loading />;
}
