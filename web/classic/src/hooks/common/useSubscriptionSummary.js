import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import { API } from '../../helpers';
import { UserContext } from '../../context/User';

const REFRESH_INTERVAL_MS = 60_000;

export const useSubscriptionSummary = () => {
  const [userState] = useContext(UserContext);
  const [subscriptions, setSubscriptions] = useState([]);
  const [loading, setLoading] = useState(false);
  const lastFetchedRef = useRef(0);
  const userId = userState?.user?.id;

  const fetchSummary = useCallback(
    async (force = false) => {
      if (!userId) {
        setSubscriptions([]);
        return;
      }
      const now = Date.now();
      if (!force && now - lastFetchedRef.current < REFRESH_INTERVAL_MS) return;
      lastFetchedRef.current = now;
      setLoading(true);
      try {
        const res = await API.get('/api/subscription/self');
        if (res?.data?.success) {
          setSubscriptions(res.data.data?.subscriptions || []);
        }
      } catch (e) {
        // ignore network/permission errors silently
      } finally {
        setLoading(false);
      }
    },
    [userId],
  );

  useEffect(() => {
    fetchSummary();
  }, [fetchSummary]);

  return {
    subscriptions,
    loading,
    refresh: () => fetchSummary(true),
  };
};
