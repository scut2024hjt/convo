import importlib.util
from pathlib import Path
import unittest

spec=importlib.util.spec_from_file_location('prepare_isolated_sql',Path(__file__).with_name('prepare_isolated_sql.py'))
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class TestIsolatedDDL(unittest.TestCase):
    def test_transforms_only_database_header(self):
        src='CREATE DATABASE IF NOT EXISTS convo;\nUSE convo;\nCREATE TABLE t (v INT);'
        out=module.render(src,'convo_t01')
        self.assertIn('DATABASE IF NOT EXISTS `convo_t01`;',out)
        self.assertIn('USE `convo_t01`;',out)
        self.assertIn('CREATE TABLE t (v INT);',out)
    def test_rejects_unsafe_names_and_unexpected_headers(self):
        src='CREATE DATABASE IF NOT EXISTS convo;\nUSE convo;'
        for name in ['convo','prod','convo_t01; DROP DATABASE users;','CONVO_T01']:
            with self.subTest(name=name),self.assertRaises(ValueError):module.render(src,name)
        with self.assertRaises(ValueError):module.render('USE convo;', 'convo_t01')

if __name__=='__main__':unittest.main()
