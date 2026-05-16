const KEY = 'axiaops_token';

export const saveToken  = (t) => localStorage.setItem(KEY, t);
export const getToken   = ()  => localStorage.getItem(KEY);
export const clearToken = ()  => localStorage.removeItem(KEY);
