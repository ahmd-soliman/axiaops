import { useNavigate } from 'react-router-dom';
import ErrorPage from '../components/ErrorPage';

export default function NotFound() {
  const navigate = useNavigate();
  return (
    <ErrorPage
      embedded
      code="404"
      title="This page isn't here"
      description="The link may be broken, or the page may have been moved. Head back to the overview and find what you were looking for from there."
      actions={[
        { label: 'Go to overview', primary: true, to: '/' },
        { label: 'Go back',                       onClick: () => navigate(-1) },
      ]}
    />
  );
}
